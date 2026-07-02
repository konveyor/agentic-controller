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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

var _ = Describe("Agent Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("when all dependencies are ready", func() {
		const (
			agentName    = "agent-ctrl-all-ready"
			providerName = "agent-ctrl-provider"
			secretName   = "agent-ctrl-secret"
			skillName    = "agent-ctrl-skill"
		)

		It("should report Ready=True", func() {
			By("creating a credential Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
				},
				StringData: map[string]string{testSecretKey: "test"},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("creating a Ready LLMProvider")
			provider := &konveyoriov1alpha1.LLMProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      providerName,
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

			// Wait for verification Job, then simulate success.
			jobKey := types.NamespacedName{
				Name:      verificationJobPrefix + providerName,
				Namespace: testNamespace,
			}
			Eventually(func(g Gomega) {
				var job batchv1.Job
				g.Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			var job batchv1.Job
			Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: "SuccessCriteriaMet", Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			// Wait for provider to become Ready.
			provKey := types.NamespacedName{Name: providerName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.LLMProvider
				g.Expect(k8sClient.Get(ctx, provKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())

			By("creating a Ready SkillCard")
			skill := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      skillName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Image: "quay.io/konveyor/skills:test-skill",
				},
			}
			Expect(k8sClient.Create(ctx, skill)).To(Succeed())

			scKey := types.NamespacedName{Name: skillName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCard
				g.Expect(k8sClient.Get(ctx, scKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())

			By("creating the Agent referencing both")
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:      testAgentImage,
					Providers:  []konveyoriov1alpha1.AgentProviderRef{{Ref: providerName}},
					SkillCards: []konveyoriov1alpha1.AgentSkillCardRef{{Ref: skillName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			By("verifying the Agent is Ready")
			agentKey := types.NamespacedName{Name: agentName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Agent
				g.Expect(k8sClient.Get(ctx, agentKey, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal("AllDependenciesReady"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
			Expect(k8sClient.Delete(ctx, skill)).To(Succeed())
			Expect(k8sClient.Delete(ctx, provider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})

	Context("when a referenced LLMProvider does not exist", func() {
		const agentName = "agent-ctrl-missing-provider"

		It("should report Ready=False with DependenciesNotReady", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:     testAgentImage,
					Providers: []konveyoriov1alpha1.AgentProviderRef{{Ref: "nonexistent-provider"}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			agentKey := types.NamespacedName{Name: agentName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Agent
				g.Expect(k8sClient.Get(ctx, agentKey, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("DependenciesNotReady"))
				g.Expect(readyCond.Message).To(ContainSubstring("not found"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when a referenced SkillCard does not exist", func() {
		const agentName = "agent-ctrl-missing-skill"

		It("should report Ready=False listing the missing SkillCard", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image: testAgentImage,
					// Need at least one provider for MinItems=1.
					Providers:  []konveyoriov1alpha1.AgentProviderRef{{Ref: testProviderName}},
					SkillCards: []konveyoriov1alpha1.AgentSkillCardRef{{Ref: "nonexistent-skill"}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			agentKey := types.NamespacedName{Name: agentName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.Agent
				g.Expect(k8sClient.Get(ctx, agentKey, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Message).To(ContainSubstring("nonexistent-skill"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})
})
