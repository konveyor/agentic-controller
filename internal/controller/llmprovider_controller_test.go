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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

var _ = Describe("LLMProvider Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("when the credential Secret does not exist", func() {
		const name = "llm-ctrl-no-secret"

		It("should set Ready=False with CredentialSecretNotFound", func() {
			provider := &konveyoriov1alpha1.LLMProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.LLMProviderSpec{
					Endpoint: testEndpoint,
					CredentialRef: konveyoriov1alpha1.LLMProviderCredentialRef{
						SecretName: "nonexistent-secret",
						Key:        testSecretKey,
					},
					Models: []konveyoriov1alpha1.LLMProviderModel{
						{Name: testLLMModelName, ContextWindow: 100000},
					},
				},
			}
			Expect(k8sClient.Create(ctx, provider)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.LLMProvider
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("CredentialSecretNotFound"))
				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, provider)).To(Succeed())
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

			provider := &konveyoriov1alpha1.LLMProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.LLMProviderSpec{
					Endpoint: testEndpoint,
					CredentialRef: konveyoriov1alpha1.LLMProviderCredentialRef{
						SecretName: secretName,
						Key:        testSecretKey,
					},
					Models: []konveyoriov1alpha1.LLMProviderModel{
						{Name: testLLMModelName, ContextWindow: 100000},
					},
				},
			}
			Expect(k8sClient.Create(ctx, provider)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.LLMProvider
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("CredentialKeyNotFound"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, provider)).To(Succeed())
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

			provider := &konveyoriov1alpha1.LLMProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.LLMProviderSpec{
					Endpoint: testEndpoint,
					CredentialRef: konveyoriov1alpha1.LLMProviderCredentialRef{
						SecretName: secretName,
						Key:        testSecretKey,
					},
					Models: []konveyoriov1alpha1.LLMProviderModel{
						{Name: testLLMModelName, ContextWindow: 100000},
					},
				},
			}
			Expect(k8sClient.Create(ctx, provider)).To(Succeed())

			By("verifying the controller creates a verification Job")
			jobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s%s-gen1", verificationJobPrefix, name),
				Namespace: testNamespace,
			}
			Eventually(func(g Gomega) {
				var job batchv1.Job
				g.Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
				g.Expect(job.Labels["konveyor.io/llmprovider"]).To(Equal(name))
			}, timeout, interval).Should(Succeed())

			By("verifying the provider is in Verifying state")
			provKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.LLMProvider
				g.Expect(k8sClient.Get(ctx, provKey, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("Verifying"))
			}, timeout, interval).Should(Succeed())

			By("simulating Job completion (success)")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: jobConditionSuccessCriteriaMet, Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("verifying the provider becomes Ready with connectionVerified")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.LLMProvider
				g.Expect(k8sClient.Get(ctx, provKey, &fetched)).To(Succeed())

				g.Expect(fetched.Status.ConnectionVerified).To(BeTrue())
				g.Expect(fetched.Status.DiscoveredModels).To(ConsistOf("test-model"))

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal("ConnectionVerified"))
			}, timeout, interval).Should(Succeed())

			By("verifying the Job is cleaned up")
			Eventually(func(g Gomega) {
				var job batchv1.Job
				err := k8sClient.Get(ctx, jobKey, &job)
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, provider)).To(Succeed())
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

			provider := &konveyoriov1alpha1.LLMProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.LLMProviderSpec{
					Endpoint: "https://api.unreachable.example.com",
					CredentialRef: konveyoriov1alpha1.LLMProviderCredentialRef{
						SecretName: secretName,
						Key:        testSecretKey,
					},
					Models: []konveyoriov1alpha1.LLMProviderModel{
						{Name: testLLMModelName, ContextWindow: 100000},
					},
				},
			}
			Expect(k8sClient.Create(ctx, provider)).To(Succeed())

			By("waiting for the verification Job to be created")
			jobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s%s-gen1", verificationJobPrefix, name),
				Namespace: testNamespace,
			}
			Eventually(func(g Gomega) {
				var job batchv1.Job
				g.Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			By("simulating Job failure")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: "FailureTarget", Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("verifying the provider is NotReady with ConnectionFailed")
			provKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.LLMProvider
				g.Expect(k8sClient.Get(ctx, provKey, &fetched)).To(Succeed())

				g.Expect(fetched.Status.ConnectionVerified).To(BeFalse())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("ConnectionFailed"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, provider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})
})
