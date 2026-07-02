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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

var _ = Describe("AgentRun Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("when the referenced Agent does not exist", func() {
		const name = "ar-ctrl-no-agent"

		It("should set Phase=Failed with AgentNotFound", func() {
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: "nonexistent-agent",
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal("AgentNotFound"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
		})
	})

	Context("when an undeclared param is supplied", func() {
		const (
			name      = "ar-ctrl-bad-param"
			agentName = "ar-ctrl-agent-for-param"
		)

		It("should set Phase=Failed with InvalidParams", func() {
			By("creating the Agent with one declared param")
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:     testAgentImage,
					Providers: []konveyoriov1alpha1.AgentProviderRef{{Ref: testProviderName}},
					Params: []konveyoriov1alpha1.AgentParam{
						{Name: testParamName, Required: true},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			By("creating an AgentRun with an undeclared param")
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
					Params: []konveyoriov1alpha1.AgentRunParam{
						{Name: testParamName, Value: testRepoURL},
						{Name: "undeclared_param", Value: "bad"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal("InvalidParams"))
				g.Expect(readyCond.Message).To(ContainSubstring("undeclared_param"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when a required param is missing", func() {
		const (
			name      = "ar-ctrl-missing-param"
			agentName = "ar-ctrl-agent-for-missing"
		)

		It("should set Phase=Failed with InvalidParams", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:     testAgentImage,
					Providers: []konveyoriov1alpha1.AgentProviderRef{{Ref: testProviderName}},
					Params: []konveyoriov1alpha1.AgentParam{
						{Name: testParamName, Required: true},
						{Name: "target_branch", Required: true},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
					Params: []konveyoriov1alpha1.AgentRunParam{
						{Name: testParamName, Value: testRepoURL},
						// target_branch is missing
					},
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Message).To(ContainSubstring("target_branch"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when params are valid", func() {
		const (
			name      = "ar-ctrl-valid"
			agentName = "ar-ctrl-agent-valid"
		)

		It("should create a Sandbox and ACP Secret", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:     testAgentImage,
					Prompt:    "You are a test agent.",
					Providers: []konveyoriov1alpha1.AgentProviderRef{{Ref: testProviderName}},
					Params: []konveyoriov1alpha1.AgentParam{
						{Name: testParamName, Required: true},
						{Name: "source_branch", Default: testDefaultBranch},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
					Params: []konveyoriov1alpha1.AgentRunParam{
						{Name: testParamName, Value: testRepoURL},
					},
					Models: []konveyoriov1alpha1.AgentRunModelSelection{
						{Role: testRolePrimary, Provider: "anthropic", Model: "claude-sonnet"},
					},
					Instructions: "Run the migration.",
					Env: []corev1.EnvVar{
						{Name: "HUB_URL", Value: "https://hub.example.com"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			By("verifying a Sandbox is created")
			sandboxKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var sandbox sandboxv1beta1.Sandbox
				g.Expect(k8sClient.Get(ctx, sandboxKey, &sandbox)).To(Succeed())

				// Verify the Sandbox has the agent image.
				g.Expect(sandbox.Spec.PodTemplate.Spec.Containers).To(HaveLen(1))
				container := sandbox.Spec.PodTemplate.Spec.Containers[0]
				g.Expect(container.Image).To(Equal(testAgentImage))

				// Verify KONVEYOR_PARAM_* env vars.
				envMap := make(map[string]string)
				for _, e := range container.Env {
					if e.Value != "" {
						envMap[e.Name] = e.Value
					}
				}
				g.Expect(envMap).To(HaveKeyWithValue("KONVEYOR_PARAM_SOURCE_URL",
					testRepoURL))
				g.Expect(envMap).To(HaveKeyWithValue("KONVEYOR_PARAM_SOURCE_BRANCH", testDefaultBranch))
				g.Expect(envMap).To(HaveKeyWithValue("KONVEYOR_INSTRUCTIONS", "Run the migration."))
				g.Expect(envMap).To(HaveKeyWithValue("KONVEYOR_PROMPT", "You are a test agent."))
				g.Expect(envMap).To(HaveKeyWithValue("KONVEYOR_MODEL_PRIMARY_PROVIDER", "anthropic"))
				g.Expect(envMap).To(HaveKeyWithValue("KONVEYOR_MODEL_PRIMARY_MODEL", "claude-sonnet"))
				g.Expect(envMap).To(HaveKeyWithValue("HUB_URL", "https://hub.example.com"))

				// Verify service is enabled.
				g.Expect(sandbox.Spec.Service).NotTo(BeNil())
				g.Expect(*sandbox.Spec.Service).To(BeTrue())

				// Verify workspace volume exists.
				hasWorkspace := false
				for _, v := range sandbox.Spec.PodTemplate.Spec.Volumes {
					if v.Name == workspaceVolumeName && v.EmptyDir != nil {
						hasWorkspace = true
					}
				}
				g.Expect(hasWorkspace).To(BeTrue(), "workspace EmptyDir volume not found")
			}, timeout, interval).Should(Succeed())

			By("verifying the ACP Secret is created")
			secretKey := types.NamespacedName{
				Name:      name + "-acp-key",
				Namespace: testNamespace,
			}
			Eventually(func(g Gomega) {
				var secret corev1.Secret
				g.Expect(k8sClient.Get(ctx, secretKey, &secret)).To(Succeed())
				g.Expect(secret.Data).To(HaveKey("secret-key"))
				g.Expect(secret.Data["secret-key"]).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			By("verifying the AgentRun status has the sandbox and secret refs")
			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, runKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.SandboxName).To(Equal(name))
				g.Expect(fetched.Status.SecretKeyRef).NotTo(BeNil())
				g.Expect(fetched.Status.SecretKeyRef.Name).To(Equal(name + "-acp-key"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when the Sandbox finishes successfully", func() {
		const (
			name      = "ar-ctrl-finish"
			agentName = "ar-ctrl-agent-finish"
		)

		It("should set Phase=Succeeded with duration", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:     testAgentImage,
					Providers: []konveyoriov1alpha1.AgentProviderRef{{Ref: testProviderName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			By("waiting for the Sandbox to be created")
			sandboxKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var sandbox sandboxv1beta1.Sandbox
				g.Expect(k8sClient.Get(ctx, sandboxKey, &sandbox)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			By("simulating Sandbox completion")
			var sandbox sandboxv1beta1.Sandbox
			Expect(k8sClient.Get(ctx, sandboxKey, &sandbox)).To(Succeed())
			sandbox.Status.Conditions = append(sandbox.Status.Conditions, metav1.Condition{
				Type:               "Finished",
				Status:             metav1.ConditionTrue,
				Reason:             sandboxFinishedReasonSucceeded,
				LastTransitionTime: metav1.Now(),
			})
			Expect(k8sClient.Status().Update(ctx, &sandbox)).To(Succeed())

			By("verifying the AgentRun reaches Succeeded phase")
			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, runKey, &fetched)).To(Succeed())

				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseSucceeded))
				g.Expect(fetched.Status.CompletionTime).NotTo(BeNil())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal(sandboxFinishedReasonSucceeded))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})
})
