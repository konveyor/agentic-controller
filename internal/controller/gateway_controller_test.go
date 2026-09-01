/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// awaitVerificationJob waits for the Gateway's verification Job and returns it.
// The Job name carries a hash of the credential (see verificationJobName), so
// tests find it by the konveyor.io/gateway label rather than reconstructing the
// name from the Gateway's generation alone. The label value is the bounded form,
// not the raw name - see gatewayLabelValue.
func awaitVerificationJob(gatewayName string) batchv1.Job {
	gw := &konveyoriov1alpha1.Gateway{}
	gw.Name = gatewayName

	var job batchv1.Job
	EventuallyWithOffset(1, func(g Gomega) {
		var jobs batchv1.JobList
		g.Expect(k8sClient.List(ctx, &jobs,
			client.InNamespace(testNamespace),
			client.MatchingLabels{labelGateway: gatewayLabelValue(gw)},
		)).To(Succeed())
		g.Expect(jobs.Items).To(HaveLen(1))
		job = jobs.Items[0]
	}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
	return job
}

var _ = Describe("Gateway Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	// verifyWithPodDiagnostic drives a full verification cycle for a valid
	// credential: it creates the Secret and Gateway, waits for the controller
	// to create the verification Job, then simulates the probe pod terminating
	// with the given diagnostic message and the Job completing (or failing).
	// It asserts the resulting Ready condition reason, message substring, and
	// connectionVerified bool. envtest has no kubelet, so the pod and its
	// terminated container status must be created by the test.
	verifyWithPodDiagnostic := func(
		name, secretName, endpoint, diag string,
		succeed bool, wantReason, wantMsgSubstr string,
	) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: testNamespace},
			StringData: map[string]string{testSecretKey: "test-key-value"},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		gateway := &konveyoriov1alpha1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec: konveyoriov1alpha1.GatewaySpec{
				Provider: testProviderType,
				Endpoint: endpoint,
				CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
					SecretName: secretName,
					Key:        testSecretKey,
				},
				Model: konveyoriov1alpha1.GatewayModel{
					Name: testLLMModelName, ContextWindow: 100000,
				},
			},
		}
		Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

		By("waiting for the verification Job to be created")
		job := awaitVerificationJob(name)
		jobName := job.Name
		jobKey := client.ObjectKeyFromObject(&job)

		By("simulating the probe pod terminating with a diagnostic")
		// Job-created pods carry both labels; the controller pins the read to
		// the Job by controller UID, so the test pod must too.
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName + "-pod",
				Namespace: testNamespace,
				Labels: map[string]string{
					batchv1.JobNameLabel:       jobName,
					batchv1.ControllerUidLabel: string(job.UID),
				},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers:    []corev1.Container{{Name: verifyContainerName, Image: "busybox"}},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
		pod.Status.Phase = corev1.PodFailed
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: verifyContainerName,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Message: diag,
			}},
		}}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

		By("simulating Job completion")
		Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
		now := metav1.Now()
		job.Status.StartTime = &now
		if succeed {
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: jobConditionSuccessCriteriaMet, Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			)
		} else {
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: "FailureTarget", Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
			)
		}
		Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

		By("verifying the Gateway surfaces the diagnostic on status")
		gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
		Eventually(func(g Gomega) {
			var fetched konveyoriov1alpha1.Gateway
			g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
			g.Expect(fetched.Status.ConnectionVerified).To(Equal(succeed))

			readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
			g.Expect(readyCond).NotTo(BeNil())
			g.Expect(readyCond.Reason).To(Equal(wantReason))
			g.Expect(readyCond.Message).To(ContainSubstring(wantMsgSubstr))
		}, timeout, interval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
		Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, pod))).To(Succeed())
	}

	Context("when the credential Secret does not exist", func() {
		const name = "llm-ctrl-no-secret"

		It("should set Ready=False with CredentialSecretNotFound", func() {
			gateway := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: testProviderType,
					Endpoint: testEndpoint,
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: "nonexistent-secret",
						Key:        testSecretKey,
					},
					Model: konveyoriov1alpha1.GatewayModel{
						Name: testLLMModelName, ContextWindow: 100000,
					},
				},
			}
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("CredentialSecretNotFound"))
				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
		})
	})

	Context("when the credential Secret exists but the key is missing", func() {
		const (
			name       = "llm-ctrl-bad-key"
			secretName = "llm-secret-bad-key"
		)

		It("should set Ready=False with CredentialKeyNotFound", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
				},
				StringData: map[string]string{
					"wrong-key": "some-value",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			gateway := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: testProviderType,
					Endpoint: testEndpoint,
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: secretName,
						Key:        testSecretKey,
					},
					Model: konveyoriov1alpha1.GatewayModel{
						Name: testLLMModelName, ContextWindow: 100000,
					},
				},
			}
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("CredentialKeyNotFound"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})

	Context("when the credential Secret is valid", func() {
		const (
			name       = "llm-ctrl-valid"
			secretName = "llm-secret-valid"
		)

		It("should create a verification Job and set Verifying status", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
				},
				StringData: map[string]string{
					testSecretKey: "test-key-value",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			gateway := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: testProviderType,
					Endpoint: testEndpoint,
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: secretName,
						Key:        testSecretKey,
					},
					Model: konveyoriov1alpha1.GatewayModel{
						Name: testLLMModelName, ContextWindow: 100000,
					},
				},
			}
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			By("verifying the controller creates a verification Job")
			createdJob := awaitVerificationJob(name)
			Expect(createdJob.Spec.Template.Spec.Containers).NotTo(BeEmpty())
			Expect(createdJob.Spec.Template.Spec.Containers[0].Command).To(ContainElement(
				ContainSubstring(verificationHTTPCodePattern),
			))

			By("verifying the gateway is in Verifying state")
			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("Verifying"))
			}, timeout, interval).Should(Succeed())

			By("simulating Job completion (success)")
			job := createdJob
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: jobConditionSuccessCriteriaMet, Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("verifying the gateway becomes Ready with connectionVerified")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())

				g.Expect(fetched.Status.ConnectionVerified).To(BeTrue())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal("ConnectionVerified"))
			}, timeout, interval).Should(Succeed())

			By("verifying the Job is cleaned up")
			Eventually(func(g Gomega) {
				var fetchedJob batchv1.Job
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(&createdJob), &fetchedJob)
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})

	Context("when the verification Job fails", func() {
		const (
			name       = "llm-ctrl-fail"
			secretName = "llm-secret-fail"
		)

		It("should set Ready=False with ConnectionFailed", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
				},
				StringData: map[string]string{
					testSecretKey: "bad-key",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			gateway := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: testProviderType,
					Endpoint: "https://api.unreachable.example.com",
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: secretName,
						Key:        testSecretKey,
					},
					Model: konveyoriov1alpha1.GatewayModel{
						Name: testLLMModelName, ContextWindow: 100000,
					},
				},
			}
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			By("waiting for the verification Job to be created")
			job := awaitVerificationJob(name)

			By("simulating Job failure")
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: "FailureTarget", Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("verifying the gateway is NotReady with ConnectionFailed")
			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())

				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("ConnectionFailed"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})

	Context("when the probe reports a specific outcome", func() {
		It("surfaces an authentication failure (HTTP 401)", func() {
			verifyWithPodDiagnostic("llm-ctrl-auth", "llm-secret-auth", testEndpoint,
				"auth code=401", false, "AuthenticationFailed", "HTTP 401")
		})

		It("surfaces a forbidden response (HTTP 403)", func() {
			verifyWithPodDiagnostic("llm-ctrl-forbidden", "llm-secret-forbidden", testEndpoint,
				"auth code=403", false, "AuthenticationFailed", "API key")
		})

		It("surfaces an unreachable endpoint (timeout)", func() {
			verifyWithPodDiagnostic("llm-ctrl-timeout", "llm-secret-timeout", testEndpoint,
				"unreachable rc=28", false, "EndpointUnreachable", "timed out")
		})

		It("surfaces a generic non-2xx response (HTTP 500)", func() {
			verifyWithPodDiagnostic("llm-ctrl-500", "llm-secret-500", testEndpoint,
				"http code=500", false, "ConnectionFailed", "HTTP 500")
		})

		It("surfaces the HTTP code on success", func() {
			verifyWithPodDiagnostic("llm-ctrl-ok", "llm-secret-ok", testEndpoint,
				"ok code=200", true, "ConnectionVerified", "HTTP 200")
		})
	})

	// The controller watches credential Secrets so a credential that changes
	// out from under a verified Gateway does not leave it reading Ready until
	// something else happens to the Gateway. See issue #103.
	Context("when the credential Secret changes after verification", func() {
		// verifyGateway creates the Secret and Gateway, drives the
		// verification Job to success, and returns them Ready.
		verifyGateway := func(name, secretName, keyValue string) (
			*konveyoriov1alpha1.Gateway, *corev1.Secret, batchv1.Job,
		) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: testNamespace},
				StringData: map[string]string{testSecretKey: keyValue},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			gateway := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: testProviderType,
					Endpoint: testEndpoint,
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: secretName,
						Key:        testSecretKey,
					},
					Model: konveyoriov1alpha1.GatewayModel{
						Name: testLLMModelName, ContextWindow: 100000,
					},
				},
			}
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			job := awaitVerificationJob(name)
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: jobConditionSuccessCriteriaMet, Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.ConnectionVerified).To(BeTrue())
				g.Expect(fetched.Status.VerifiedCredentialHash).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			return gateway, secret, job
		}

		It("marks the Gateway NotReady when the Secret is deleted", func() {
			const (
				name       = "llm-ctrl-secret-deleted"
				secretName = "llm-secret-deleted"
			)
			gateway, secret, _ := verifyGateway(name, secretName, credOriginal)

			By("deleting the credential Secret")
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())

			By("verifying the Secret watch brings the Gateway back to NotReady")
			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("CredentialSecretNotFound"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
		})

		It("verifies a Gateway whose name is too long to be a label value", func() {
			// A Gateway name is a DNS subdomain, so 64 characters is valid. The
			// verification Job's labelGateway value is not - it caps at 63, and
			// the raw name would have the API server reject the Job on create.
			name := strings.Repeat("g", 64)
			const secretName = "llm-secret-long-name"

			gateway, secret, job := verifyGateway(name, secretName, credOriginal)

			Expect(len(name)).To(BeNumerically(">", 63))
			Expect(validation.IsValidLabelValue(job.Labels[labelGateway])).To(BeEmpty())
			Expect(validation.IsDNS1123Label(job.Name)).To(BeEmpty())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		It("re-verifies when the credential is rotated", func() {
			const (
				name       = "llm-ctrl-secret-rotated"
				secretName = "llm-secret-rotated"
			)
			gateway, secret, firstJob := verifyGateway(name, secretName, credOriginal)

			By("rotating the credential in place")
			// The generation is unchanged, so only the credential hash can
			// tell the controller the settled result no longer applies.
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)).To(Succeed())
			secret.StringData = map[string]string{testSecretKey: credRotated}
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())

			By("verifying a fresh verification Job is created under a new name")
			Eventually(func(g Gomega) {
				var jobs batchv1.JobList
				gw := &konveyoriov1alpha1.Gateway{}
				gw.Name = name
				g.Expect(k8sClient.List(ctx, &jobs,
					client.InNamespace(testNamespace),
					client.MatchingLabels{labelGateway: gatewayLabelValue(gw)},
				)).To(Succeed())
				names := make([]string, len(jobs.Items))
				for i, j := range jobs.Items {
					names[i] = j.Name
				}
				g.Expect(names).To(ContainElement(Not(Equal(firstJob.Name))))
			}, timeout, interval).Should(Succeed())

			By("verifying the Gateway is back in Verifying")
			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal("Verifying"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})
})
