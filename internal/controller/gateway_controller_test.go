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
				batchv1.JobCondition{Type: jobConditionFailureTarget, Status: corev1.ConditionTrue},
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
				batchv1.JobCondition{Type: jobConditionFailureTarget, Status: corev1.ConditionTrue},
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

	// A native gcp-vertex-ai Gateway has no OpenAI-compatible /v1/models, and
	// reaching it needs an OAuth2 token minted from a service account that
	// nothing in the Job can mint. A probe could only ever fail and park it at
	// ConnectionFailed forever, holding every referencing Agent at
	// DependenciesNotReady. It is settled without a probe instead.
	Context("when the provider has no probe the controller can speak", func() {
		const (
			name       = "llm-ctrl-vertex"
			secretName = "llm-secret-vertex"
		)

		It("should set Ready=True with VerificationSkipped and create no Job", func() {
			secret, gateway := newVertexGatewayFixture(name, secretName)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			By("verifying the Gateway settles as Ready without being probed")
			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal(reasonVerificationSkipped))
				g.Expect(readyCond.Message).To(ContainSubstring(testProviderVertex))
				// Ready, but honest: nothing was actually checked.
				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())
			}, timeout, interval).Should(Succeed())

			By("verifying no verification Job is ever created")
			gwLabel := &konveyoriov1alpha1.Gateway{}
			gwLabel.Name = name
			Consistently(func(g Gomega) {
				var jobs batchv1.JobList
				g.Expect(k8sClient.List(ctx, &jobs,
					client.InNamespace(testNamespace),
					client.MatchingLabels{labelGateway: gatewayLabelValue(gwLabel)},
				)).To(Succeed())
				g.Expect(jobs.Items).To(BeEmpty())
			}, 2*time.Second, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		// A controller from before the skip parks a native Gateway at
		// ConnectionFailed. That reason is terminal for the generation and
		// credential it was reached against, so an upgraded controller must
		// not take the "already settled" early return on it - otherwise the
		// stale verdict outlives the upgrade and every referencing Agent stays
		// at DependenciesNotReady until someone edits the spec (#227).
		It("should replace a stale ConnectionFailed verdict left by an older controller", func() {
			const (
				staleName       = "llm-ctrl-vertex-stale"
				staleSecretName = "llm-secret-vertex-stale"
			)
			secret, gateway := newVertexGatewayFixture(staleName, staleSecretName)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			gwKey := types.NamespacedName{Name: staleName, Namespace: testNamespace}
			By("letting the Gateway settle as skipped first")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal(reasonVerificationSkipped))
			}, timeout, interval).Should(Succeed())

			By("overwriting status with the verdict an older controller would have left")
			// Same generation, same verifiedCredentialHash (the skip recorded
			// it), terminal reason: exactly the shape the early return keys on.
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				fetched.Status.ConnectionVerified = false
				meta.SetStatusCondition(&fetched.Status.Conditions, metav1.Condition{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: fetched.Generation,
					Reason:             reasonConnectionFailed,
					Message:            "Endpoint https://aiplatform.googleapis.com/v1/models returned HTTP 404",
				})
				g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			By("verifying the reconcile replaces it with VerificationSkipped")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal(reasonVerificationSkipped))
				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})

	// The skip wording changed with this probe, and a Gateway skipped by an
	// older controller is settled against the old sentence. On the reason
	// alone it would keep saying "/v1/models" - a surface that no longer
	// decides anything - until its generation or credential changed.
	Context("when a skipped Gateway carries an older controller's wording", func() {
		It("should refresh the skip message in place", func() {
			const (
				name       = "llm-ctrl-vertex-wording"
				secretName = "llm-secret-vertex-wording"
			)
			secret, gateway := newVertexGatewayFixture(name, secretName)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			const staleMessage = "Provider \"gcp-vertex-ai\" has no OpenAI-compatible " +
				"/v1/models to probe; endpoint and credentials are unverified"

			By("letting it settle, then rewriting the message an older controller wrote")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal(reasonVerificationSkipped))
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				meta.SetStatusCondition(&fetched.Status.Conditions, metav1.Condition{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: fetched.Generation,
					Reason:             reasonVerificationSkipped,
					Message:            staleMessage,
				})
				g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			By("verifying the reconcile replaces it with the current wording")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal(reasonVerificationSkipped))
				g.Expect(readyCond.Message).NotTo(Equal(staleMessage))
				g.Expect(readyCond.Message).To(ContainSubstring("no API surface"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})

	// A native aws-bedrock Gateway gets a SigV4 ConverseStream probe (#304),
	// so bad credentials are caught here rather than mid-run as a bare
	// "Authentication required".
	Context("when the provider is aws-bedrock", func() {
		It("should replay a controller-signed ConverseStream and never mount the credential", func() {
			const (
				name       = "llm-ctrl-bedrock"
				secretName = "llm-secret-bedrock"
			)
			secret, gateway := newBedrockGatewayFixture(name, secretName)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			By("verifying the pod replays a signature rather than computing one")
			job := awaitVerificationJob(name)
			container := job.Spec.Template.Spec.Containers[0]
			Expect(container.Command).To(HaveLen(3))
			// curl 8.12.1, which this image ships, signs SigV4's canonical URI
			// wrong, so the probe must not sign at all. The operation and the
			// model id are no longer in the command either - they are inside
			// the signed URL, asserted below.
			Expect(container.Command[2]).NotTo(ContainSubstring("--aws-sigv4"))
			Expect(container.Command[2]).To(ContainSubstring("$" + envProbeURL))

			env := map[string]string{}
			for _, e := range container.Env {
				env[e.Name] = e.Value
			}
			// The host is the one the run reaches: built from the Secret's
			// AWS_REGION, the same value the signature is scoped to.
			Expect(env[envProbeURL]).To(HavePrefix(
				"https://bedrock-runtime." + testAWSRegion + ".amazonaws.com/model/"))
			Expect(env[envProbeURL]).To(HaveSuffix("/converse-stream"))
			Expect(env[envSigV4Authorization]).To(ContainSubstring(
				"Credential=AKIAEXAMPLE/"))
			Expect(env[envSigV4Authorization]).To(ContainSubstring(
				"/" + testAWSRegion + "/bedrock/aws4_request"))
			Expect(env[envSigV4Date]).NotTo(BeEmpty())

			// The Secret is not mounted at all any more, and the secret
			// access key never leaves the controller.
			Expect(container.EnvFrom).To(BeEmpty())
			for _, e := range container.Env {
				Expect(e.Value).NotTo(ContainSubstring(testAWSSecretKey))
			}
			// A static credential has no session token to forward.
			Expect(env).NotTo(HaveKey(envSigV4SecurityToken))

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		// Regression from review: an inference-profile ARN contains slashes,
		// and raw in the path they split it so bedrock-runtime stops routing
		// to ConverseStream - it answers HTTP 200 with a coral
		// UnknownOperationException, which the probe recorded as verified.
		It("should encode an inference-profile ARN model id as one path segment", func() {
			const (
				name       = "llm-ctrl-bedrock-arn"
				secretName = "llm-secret-bedrock-arn"
				modelARN   = "arn:aws:bedrock:us-east-1:123456789012:" +
					"inference-profile/us.anthropic.claude-sonnet-4-5-20250929-v1:0"
			)
			secret, gateway := newBedrockGatewayFixture(name, secretName)
			gateway.Spec.Model.Name = modelARN
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			job := awaitVerificationJob(name)
			container := job.Spec.Template.Spec.Containers[0]

			var probeURL string
			for _, e := range container.Env {
				if e.Name == envProbeURL {
					probeURL = e.Value
				}
			}
			// One path label: the ARN's own slashes are escaped, so the only
			// separators left are the ones framing /model/<id>/converse-stream.
			Expect(probeURL).NotTo(BeEmpty())
			Expect(probeURL).To(ContainSubstring(
				"/model/" + awsEscapePathSegment(modelARN) + "/converse-stream"))
			Expect(probeURL).NotTo(ContainSubstring(modelARN))

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		// A Bedrock run ignores spec.endpoint - the AWS SDK builds the host
		// from AWS_REGION - so the probe must too. Signing with the Secret's
		// region against a host taken from the spec is how a working Gateway
		// gets parked at AuthenticationFailed: AWS rejects a signature whose
		// credential scope does not match the endpoint it reached, and every
		// Agent referencing the Gateway then sits at DependenciesNotReady.
		It("should probe the credential's region, not the endpoint's", func() {
			const (
				name       = "llm-ctrl-bedrock-region"
				secretName = "llm-secret-bedrock-region"
			)
			secret, gateway := newBedrockGatewayFixture(name, secretName)
			// The sample's endpoint, left alone, with a credential elsewhere.
			secret.StringData[awsRegion] = "eu-west-1"
			gateway.Spec.Endpoint = bedrockEndpoint
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			By("verifying the probe is still run, and against eu-west-1")
			job := awaitVerificationJob(name)
			container := job.Spec.Template.Spec.Containers[0]
			Expect(container.Env).To(ContainElement(corev1.EnvVar{
				Name:  envLLMEndpoint,
				Value: "https://bedrock-runtime.eu-west-1.amazonaws.com",
			}))

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		// The failure from #304: the credential was valid but an SCP denied
		// bedrock:InvokeModel*, and nothing in the chain said so. AWS's own
		// sentence has to reach the Gateway status.
		It("should surface an IAM/SCP denial as AuthenticationFailed naming the operation", func() {
			const (
				name       = "llm-ctrl-bedrock-denied"
				secretName = "llm-secret-bedrock-denied"
			)
			secret, gateway := newBedrockGatewayFixture(name, secretName)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			job := awaitVerificationJob(name)
			failVerificationJob(job, `auth code=403 detail={"message":"User: `+
				`arn:aws:iam::123456789012:user/agentic is not authorized to perform: `+
				`bedrock:InvokeModelWithResponseStream on the inference profile `+
				`us.anthropic.claude-sonnet-4-5-20250929-v1:0"}`)

			By("verifying the Gateway says what AWS said")
			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal(reasonAuthenticationFailed))
				g.Expect(readyCond.Message).To(ContainSubstring("bedrock:InvokeModelWithResponseStream"))
				g.Expect(readyCond.Message).To(ContainSubstring("arn:aws:iam::123456789012:user/agentic"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		// The probe needs the static SigV4 keys to sign. A Secret without
		// them is a setup the probe cannot reproduce (IRSA, or just a missing
		// AWS_REGION), which must stay a skip rather than become a failure.
		It("should skip, not fail, when the Secret lacks the keys the probe signs with", func() {
			const (
				name       = "llm-ctrl-bedrock-noregion"
				secretName = "llm-secret-bedrock-noregion"
			)
			secret, gateway := newBedrockGatewayFixture(name, secretName)
			delete(secret.StringData, awsRegion)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal(reasonVerificationSkipped))
				g.Expect(readyCond.Message).To(ContainSubstring(awsRegion))
				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		// Every native Bedrock Gateway on a controller from before this
		// change is settled at VerificationSkipped for its current generation
		// and credential. That reason is terminal, so without an explicit
		// exception the upgrade would never probe any of them.
		It("should re-probe a Gateway an older controller settled as VerificationSkipped", func() {
			const (
				name       = "llm-ctrl-bedrock-upgrade"
				secretName = "llm-secret-bedrock-upgrade"
			)
			secret, gateway := newBedrockGatewayFixture(name, secretName)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, gateway)).To(Succeed())

			By("letting it reach a settled verdict, then overwriting it with the old one")
			job := awaitVerificationJob(name)
			failVerificationJob(job, "auth code=403")

			gwKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				g.Expect(meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady).Reason).
					To(Equal(reasonAuthenticationFailed))
			}, timeout, interval).Should(Succeed())

			// Same generation, same verifiedCredentialHash, terminal reason:
			// exactly the shape the settled early return keys on.
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				meta.SetStatusCondition(&fetched.Status.Conditions, metav1.Condition{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: fetched.Generation,
					Reason:             reasonVerificationSkipped,
					Message:            "Provider \"aws-bedrock\" has no OpenAI-compatible /v1/models to probe",
				})
				g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			By("verifying the controller probes it instead of carrying the skip forward")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Gateway
				g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
				g.Expect(meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady).Reason).
					NotTo(Equal(reasonVerificationSkipped))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, gateway)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})
})

// failVerificationJob simulates the probe pod terminating with a diagnostic
// and its Job failing. envtest has no kubelet, so the pod and its terminated
// container status have to be created by the test.
func failVerificationJob(job batchv1.Job, diag string) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name + "-pod",
			Namespace: testNamespace,
			Labels: map[string]string{
				batchv1.JobNameLabel:       job.Name,
				batchv1.ControllerUidLabel: string(job.UID),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers:    []corev1.Container{{Name: verifyContainerName, Image: "busybox"}},
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, pod)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
	pod.Status.Phase = corev1.PodFailed
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:  verifyContainerName,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Message: diag}},
	}}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, pod)).To(Succeed())

	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(&job), &job)).To(Succeed())
	now := metav1.Now()
	job.Status.StartTime = &now
	job.Status.Conditions = append(job.Status.Conditions,
		batchv1.JobCondition{Type: jobConditionFailureTarget, Status: corev1.ConditionTrue},
		batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
	)
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, &job)).To(Succeed())
}

// newVertexGatewayFixture returns a native gcp-vertex-ai Gateway and the
// Secret it points at, unsaved. Its credential is an ADC service account, so
// the credentialRef is keyless and no probe the controller can run would say
// anything true about it. The provider is hyphenated, as users write it: the
// skip decision must go through normalizeProvider.
func newVertexGatewayFixture(name, secretName string) (*corev1.Secret, *konveyoriov1alpha1.Gateway) {
	secret, gateway := newBedrockGatewayFixture(name, secretName)
	secret.StringData = map[string]string{
		"GOOGLE_APPLICATION_CREDENTIALS_JSON": `{"type":"service_account"}`,
	}
	gateway.Spec.Provider = testProviderVertex
	gateway.Spec.Endpoint = "https://aiplatform.googleapis.com"
	return secret, gateway
}

// newBedrockGatewayFixture returns a native aws-bedrock Gateway and the
// Secret it points at, unsaved. SigV4 spans three variables, so the
// credentialRef is keyless and the whole Secret is the credential. The
// provider is hyphenated, as users write it: the probe decision must go
// through normalizeProvider.
func newBedrockGatewayFixture(name, secretName string) (*corev1.Secret, *konveyoriov1alpha1.Gateway) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: testNamespace,
		},
		StringData: map[string]string{
			awsKeyID:              "AKIAEXAMPLE",
			awsSecretAccessKeyEnv: testAWSSecretKey,
			awsRegion:             testAWSRegion,
		},
	}
	gateway := &konveyoriov1alpha1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: konveyoriov1alpha1.GatewaySpec{
			Provider: testProviderBedrock,
			Endpoint: bedrockEndpoint,
			CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
				SecretName: secretName,
			},
			Model: konveyoriov1alpha1.GatewayModel{
				Name: testLLMModelName, ContextWindow: 100000,
			},
		},
	}
	return secret, gateway
}
