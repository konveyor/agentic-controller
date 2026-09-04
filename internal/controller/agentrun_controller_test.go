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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// makeReadyGateway creates a Gateway with a verified single-key
// credential and simulates successful verification. Returns cleanup function.
func makeReadyGateway(gwName, secretName string) func() {
	return makeReadyGatewayWithCred(gwName, secretName,
		map[string]string{testSecretKey: "test-value"}, testSecretKey)
}

// makeReadyGatewayKeyless is makeReadyGateway with a keyless credentialRef
// over a multi-variable (SigV4-style) Secret.
func makeReadyGatewayKeyless(gwName, secretName string) func() {
	return makeReadyGatewayWithCred(gwName, secretName, map[string]string{
		"AWS_ACCESS_KEY_ID":     "test-access-key",
		"AWS_SECRET_ACCESS_KEY": "test-secret-key",
		"AWS_REGION":            "us-east-1",
	}, "")
}

func makeReadyGatewayWithCred(gwName, secretName string, stringData map[string]string, key string) func() {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: testNamespace},
		StringData: stringData,
	}
	ExpectWithOffset(2, k8sClient.Create(ctx, secret)).To(Succeed())

	gateway := &konveyoriov1alpha1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: gwName, Namespace: testNamespace},
		Spec: konveyoriov1alpha1.GatewaySpec{
			Provider:      testProviderType,
			Endpoint:      testEndpoint,
			CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{SecretName: secretName, Key: key},
			Model:         konveyoriov1alpha1.GatewayModel{Name: testLLMModelName, ContextWindow: 100000},
		},
	}
	ExpectWithOffset(2, k8sClient.Create(ctx, gateway)).To(Succeed())

	// Wait for verification Job, simulate success.
	job := awaitVerificationJob(gwName)
	now := metav1.Now()
	job.Status.StartTime = &now
	job.Status.CompletionTime = &now
	job.Status.Conditions = append(job.Status.Conditions,
		batchv1.JobCondition{Type: jobConditionSuccessCriteriaMet, Status: corev1.ConditionTrue},
		batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
	)
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, &job)).To(Succeed())

	// Wait for gateway to become Ready.
	gwKey := types.NamespacedName{Name: gwName, Namespace: testNamespace}
	EventuallyWithOffset(1, func(g Gomega) {
		var fetched konveyoriov1alpha1.Gateway
		g.Expect(k8sClient.Get(ctx, gwKey, &fetched)).To(Succeed())
		readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
		g.Expect(readyCond).NotTo(BeNil())
		g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
	}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

	return func() {
		k8sClient.Delete(ctx, gateway) //nolint:errcheck
		k8sClient.Delete(ctx, secret)  //nolint:errcheck
	}
}

// waitForAgentReady waits until the named Agent has Ready=True.
func waitForAgentReady(agentName string) {
	agentKey := types.NamespacedName{Name: agentName, Namespace: testNamespace}
	EventuallyWithOffset(1, func(g Gomega) {
		var fetched konveyoriov1alpha1.Agent
		g.Expect(k8sClient.Get(ctx, agentKey, &fetched)).To(Succeed())
		readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
		g.Expect(readyCond).NotTo(BeNil())
		g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
	}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
}

var _ = Describe("AgentRun Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("when the referenced Agent does not exist", func() {
		const name = "ar-ctrl-no-agent"

		It("should set Phase=Failed with AgentNotFound", func() {
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: testNonexistentAgent},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				cond := meta.FindStatusCondition(fetched.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("AgentNotFound"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
		})
	})

	Context("when a finished run sets spec.ttlSecondsAfterFinished", func() {
		const name = "ar-ctrl-ttl-gc"

		It("should garbage-collect the run after the TTL elapses", func() {
			// A nonexistent agent drives the run straight to a terminal
			// Failed phase without a Sandbox; with a short TTL the controller
			// then anchors CompletionTime and deletes the run. Only the GC
			// outcome is asserted — the transient terminal state is deleted
			// too quickly to observe reliably, and eventual deletion can only
			// happen once the run has gone terminal and been anchored.
			ttl := int32(1)
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef:                testNonexistentAgent,
					TTLSecondsAfterFinished: &ttl,
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				err := k8sClient.Get(ctx, key, &fetched)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected AgentRun to be garbage-collected")
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("when a finished run has no TTL configured", func() {
		const name = "ar-ctrl-no-ttl"

		It("should keep the run after it finishes", func() {
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: testNonexistentAgent},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
			}, timeout, interval).Should(Succeed())

			// With no TTL the run must still exist after a grace period.
			Consistently(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
			}, 2*time.Second, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
		})
	})

	Context("when managed children outlive their AgentRun", func() {
		const name = "ar-ctrl-orphaned-children"

		It("should sweep the Sandbox, Pod, ConfigMap, and Secret", func() {
			managedLabels := map[string]string{
				labelManagedBy: managedByLabel,
				labelAgentRun:  name,
			}

			children := []client.Object{
				&sandboxv1beta1.Sandbox{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, Labels: managedLabels},
					Spec: sandboxv1beta1.SandboxSpec{SandboxBlueprint: sandboxv1beta1.SandboxBlueprint{PodTemplate: sandboxv1beta1.PodTemplate{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers:    []corev1.Container{{Name: agentContainerName, Image: testAgentImage}},
						},
					}}},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, Labels: managedLabels},
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers:    []corev1.Container{{Name: agentContainerName, Image: testAgentImage}},
					},
				},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
					Name: name + "-params", Namespace: testNamespace, Labels: managedLabels,
				}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name: name + "-acp-key", Namespace: testNamespace, Labels: managedLabels,
				}},
			}

			for _, child := range children {
				Expect(k8sClient.Create(ctx, child)).To(Succeed())
			}

			for _, child := range children {
				key := client.ObjectKeyFromObject(child)
				Eventually(func(g Gomega) {
					fresh := child.DeepCopyObject().(client.Object)
					err := k8sClient.Get(ctx, key, fresh)
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
						"expected orphaned %T %s to be swept", child, key)
				}, timeout, interval).Should(Succeed())
			}
		})

		It("should sweep a Pod controlled by the missing Sandbox", func() {
			controller := true
			runName := name + "-sandbox-owned-pod"
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      runName,
					Namespace: testNamespace,
					Labels: map[string]string{
						labelManagedBy: managedByLabel,
						labelAgentRun:  runName,
					},
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: sandboxv1beta1.GroupVersion.String(),
						Kind:       sandboxKind,
						Name:       runName,
						UID:        types.UID("missing-sandbox-uid"),
						Controller: &controller,
					}},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: agentContainerName, Image: testAgentImage}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			Eventually(func() bool {
				var fresh corev1.Pod
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), &fresh)
				return apierrors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})

		It("should preserve a Pod whose Sandbox is controlled by something else", func() {
			controller := true
			runName := name + "-foreign-sandbox"
			managedLabels := map[string]string{
				labelManagedBy: managedByLabel,
				labelAgentRun:  runName,
			}
			sandbox := &sandboxv1beta1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      runName,
					Namespace: testNamespace,
					Labels:    managedLabels,
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "foreign-controller",
						UID:        types.UID("foreign-controller-uid"),
						Controller: &controller,
					}},
				},
				Spec: sandboxv1beta1.SandboxSpec{SandboxBlueprint: sandboxv1beta1.SandboxBlueprint{PodTemplate: sandboxv1beta1.PodTemplate{
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers:    []corev1.Container{{Name: agentContainerName, Image: testAgentImage}},
					},
				}}},
			}
			Expect(k8sClient.Create(ctx, sandbox)).To(Succeed())

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      runName,
					Namespace: testNamespace,
					Labels:    managedLabels,
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: sandboxv1beta1.GroupVersion.String(),
						Kind:       sandboxKind,
						Name:       sandbox.Name,
						UID:        sandbox.UID,
						Controller: &controller,
					}},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: agentContainerName, Image: testAgentImage}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			Consistently(func(g Gomega) {
				var freshSandbox sandboxv1beta1.Sandbox
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sandbox), &freshSandbox)).To(Succeed())
				var freshPod corev1.Pod
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), &freshPod)).To(Succeed())
			}, 2*time.Second, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sandbox)).To(Succeed())
		})

		It("should not delete a labeled resource controlled by something else", func() {
			controller := true
			foreignRunName := name + "-foreign-run"
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-foreign",
				Namespace: testNamespace,
				Labels: map[string]string{
					labelManagedBy: managedByLabel,
					labelAgentRun:  foreignRunName,
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "foreign-controller",
					UID:        types.UID("foreign-controller-uid"),
					Controller: &controller,
				}},
			}}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// An ownerless sibling triggers the missing-parent sweep. This makes
			// the assertion below exercise the deletion guard rather than merely
			// relying on the ownerless watch predicate to ignore the Secret.
			trigger := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-foreign-trigger",
				Namespace: testNamespace,
				Labels: map[string]string{
					labelManagedBy: managedByLabel,
					labelAgentRun:  foreignRunName,
				},
			}}
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			Eventually(func() bool {
				var fresh corev1.ConfigMap
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(trigger), &fresh)
				return apierrors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())

			key := client.ObjectKeyFromObject(secret)
			Consistently(func(g Gomega) {
				var fresh corev1.Secret
				g.Expect(k8sClient.Get(ctx, key, &fresh)).To(Succeed())
			}, 2*time.Second, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})

	Context("when the Agent is not Ready", func() {
		const (
			name      = "ar-ctrl-agent-not-ready"
			agentName = "ar-ctrl-unready-agent"
		)

		It("should set AgentNotReady and not create a Sandbox", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: "nonexistent-llm"}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: agentName},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, runKey, &fetched)).To(Succeed())
				cond := meta.FindStatusCondition(fetched.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
				g.Expect(cond.Reason).To(Equal("AgentNotReady"))
				g.Expect(fetched.Status.SandboxName).To(BeEmpty())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when an undeclared param is supplied", func() {
		const (
			name       = "ar-ctrl-bad-param"
			agentName  = "ar-ctrl-agent-badp"
			gwName     = "ar-prov-badp"
			secretName = "ar-secret-badp"
		)

		It("should set Phase=Failed with InvalidParams", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
					Params:   []konveyoriov1alpha1.Param{{Name: testParamName, Required: true}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
					Params: []konveyoriov1alpha1.ParamValue{
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
				cond := meta.FindStatusCondition(fetched.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("InvalidParams"))
				g.Expect(cond.Message).To(ContainSubstring("undeclared_param"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when a gateway is not in the Agent's gateway list", func() {
		const (
			name       = "ar-ctrl-bad-gw"
			agentName  = "ar-ctrl-agent-badgw"
			gwName     = "ar-prov-badgw"
			secretName = "ar-secret-badgw"
		)

		It("should set Phase=Failed with InvalidGateway", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
					Gateway:  "wrong-gateway",
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				cond := meta.FindStatusCondition(fetched.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("InvalidGateway"))
				g.Expect(cond.Message).To(ContainSubstring("wrong-gateway"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when all validations pass", func() {
		const (
			name       = "ar-ctrl-sandbox-create"
			agentName  = "ar-ctrl-agent-sandbox"
			gwName     = "ar-prov-sandbox"
			secretName = "ar-secret-sandbox"
			skillName  = "ar-skill-sandbox"
		)

		It("should create a Sandbox with skills and LLM credentials", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			By("creating a Ready SkillCard")
			skill := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{Name: skillName, Namespace: testNamespace},
				Spec:       konveyoriov1alpha1.SkillCardSpec{Image: testSkillImage},
			}
			Expect(k8sClient.Create(ctx, skill)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCard
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: skillName, Namespace: testNamespace}, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())

			By("creating the Agent")
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:      testAgentImage,
					Prompt:     "You are a test agent.",
					Gateways:   []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
					SkillCards: []konveyoriov1alpha1.AgentSkillCardRef{{Ref: skillName}},
					Params: []konveyoriov1alpha1.Param{
						{Name: testParamName, Required: true},
						{Name: "source_branch", Default: testDefaultBranch},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			By("creating the AgentRun")
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef:     agentName,
					Params:       []konveyoriov1alpha1.ParamValue{{Name: testParamName, Value: testRepoURL}},
					Gateway:      gwName,
					Instructions: "Run the migration.",
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			By("verifying the Sandbox is created with correct config")
			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			var fetchedRun konveyoriov1alpha1.AgentRun
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, runKey, &fetchedRun)).To(Succeed())
				g.Expect(fetchedRun.Status.SandboxName).NotTo(BeEmpty())
				g.Expect(fetchedRun.Status.SecretKeyRef).NotTo(BeNil())
			}, timeout, interval).Should(Succeed())

			By("verifying the sandbox pod template carries the run/agent labels")
			var sandbox sandboxv1beta1.Sandbox
			sandboxKey := types.NamespacedName{Name: fetchedRun.Status.SandboxName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, sandboxKey, &sandbox)).To(Succeed())
			Expect(isOwnedBy(&sandbox, &fetchedRun)).To(BeTrue(), "Sandbox must be owned by its AgentRun")
			Expect(sandbox.Labels).To(HaveKeyWithValue(labelManagedBy, managedByLabel))
			Expect(sandbox.Spec.PodTemplate.ObjectMeta.Labels).To(HaveKeyWithValue("konveyor.io/agentrun", name))
			Expect(sandbox.Spec.PodTemplate.ObjectMeta.Labels).To(HaveKeyWithValue("konveyor.io/agent", agentName))
			Expect(sandbox.Spec.PodTemplate.ObjectMeta.Labels).To(HaveKeyWithValue(labelManagedBy, managedByLabel))

			By("verifying per-run Secret and ConfigMap ownership")
			var acpSecret corev1.Secret
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: fetchedRun.Status.SecretKeyRef.Name, Namespace: testNamespace,
			}, &acpSecret)).To(Succeed())
			Expect(isOwnedBy(&acpSecret, &fetchedRun)).To(BeTrue(), "ACP Secret must be owned by its AgentRun")
			var paramsConfigMap corev1.ConfigMap
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-params", Namespace: testNamespace,
			}, &paramsConfigMap)).To(Succeed())
			Expect(isOwnedBy(&paramsConfigMap, &fetchedRun)).To(BeTrue(), "params ConfigMap must be owned by its AgentRun")

			By("verifying restartPolicy is Never so failed stages are observable (#51)")
			Expect(sandbox.Spec.PodTemplate.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))

			By("verifying /tmp EmptyDir volume is present for writable temp space")
			spec := sandbox.Spec.PodTemplate.Spec
			var tmpVolFound bool
			for _, v := range spec.Volumes {
				if v.Name == tmpVolumeName {
					tmpVolFound = true
					Expect(v.VolumeSource.EmptyDir).NotTo(BeNil())
					Expect(v.VolumeSource.EmptyDir.SizeLimit).NotTo(BeNil())
				}
			}
			Expect(tmpVolFound).To(BeTrue(), "expected a 'tmp' EmptyDir volume")

			var tmpMountFound bool
			for _, m := range spec.Containers[0].VolumeMounts {
				if m.Name == tmpVolumeName {
					tmpMountFound = true
					Expect(m.MountPath).To(Equal("/tmp"))
					Expect(m.ReadOnly).To(BeFalse())
				}
			}
			Expect(tmpMountFound).To(BeTrue(), "expected a volume mount for /tmp")

			By("verifying workspace EmptyDir volume is present")
			var wsVolFound bool
			for _, v := range spec.Volumes {
				if v.Name == "workspace" {
					wsVolFound = true
					Expect(v.VolumeSource.EmptyDir).NotTo(BeNil())
					Expect(v.VolumeSource.EmptyDir.SizeLimit).NotTo(BeNil())
				}
			}
			Expect(wsVolFound).To(BeTrue(), "expected a 'workspace' EmptyDir volume")

			By("verifying the single-key gateway credential is injected as API_KEY")
			container := sandbox.Spec.PodTemplate.Spec.Containers[0]
			var apiKey *corev1.EnvVar
			for i := range container.Env {
				if container.Env[i].Name == "KONVEYOR_LLM_API_KEY" {
					apiKey = &container.Env[i]
				}
			}
			Expect(apiKey).NotTo(BeNil())
			Expect(apiKey.ValueFrom.SecretKeyRef.Name).To(Equal(secretName))
			Expect(apiKey.ValueFrom.SecretKeyRef.Key).To(Equal(testSecretKey))

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
			Expect(k8sClient.Delete(ctx, skill)).To(Succeed())
		})
	})

	Context("when the Agent declares no gateways and the run names one", func() {
		const (
			name       = "ar-ctrl-empty-list-ok"
			agentName  = "ar-ctrl-agent-emptylist"
			gwName     = "ar-prov-emptylist"
			secretName = "ar-secret-emptylist"
		)

		It("should accept the run's gateway unconstrained and create a Sandbox", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			By("creating an Agent with no gateways")
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image: testAgentImage,
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			By("creating a run that names the gateway itself")
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
					Gateway:  gwName,
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, runKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.SandboxName).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when the Agent declares no gateways and the run names a nonexistent one", func() {
		const (
			name      = "ar-ctrl-empty-list-badgw"
			agentName = "ar-ctrl-agent-emptylist-badgw"
			gwName    = "ar-prov-does-not-exist"
		)

		It("should wait with GatewayNotFound and retry rather than fail terminally", func() {
			By("creating an Agent with no gateways")
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image: testAgentImage,
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			By("creating a run that names a gateway that does not exist")
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
					Gateway:  gwName,
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			By("observing a non-terminal GatewayNotFound wait (retried with backoff)")
			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).NotTo(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				cond := meta.FindStatusCondition(fetched.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
				g.Expect(cond.Reason).To(Equal("GatewayNotFound"))
			}, timeout, interval).Should(Succeed())

			By("recovering once the Gateway is created")
			cleanup := makeReadyGateway(gwName, "ar-secret-badgw-recover")
			defer cleanup()
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.SandboxName).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when the Agent declares no gateways and the run omits one", func() {
		const (
			name      = "ar-ctrl-empty-list-nogw"
			agentName = "ar-ctrl-agent-emptylist-nogw"
		)

		It("should fail with a message telling the run to name a gateway", func() {
			By("creating an Agent with no gateways")
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image: testAgentImage,
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				cond := meta.FindStatusCondition(fetched.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("InvalidGateway"))
				g.Expect(cond.Message).To(ContainSubstring("declares no gateways"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when the gateway has a keyless credentialRef", func() {
		const (
			name       = "ar-ctrl-keyless-cred"
			agentName  = "ar-ctrl-agent-keyless"
			gwName     = "ar-prov-keyless"
			secretName = "ar-secret-keyless"
		)

		It("should expose the credential Secret via envFrom instead of API_KEY", func() {
			cleanup := makeReadyGatewayKeyless(gwName, secretName)
			defer cleanup()

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName,
					Gateway:  gwName,
					EnvFrom: []corev1.EnvFromSource{
						{ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "user-extra-env"},
						}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			var fetchedRun konveyoriov1alpha1.AgentRun
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, runKey, &fetchedRun)).To(Succeed())
				g.Expect(fetchedRun.Status.SandboxName).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			var sandbox sandboxv1beta1.Sandbox
			sandboxKey := types.NamespacedName{Name: fetchedRun.Status.SandboxName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, sandboxKey, &sandbox)).To(Succeed())
			container := sandbox.Spec.PodTemplate.Spec.Containers[0]

			By("not injecting a single-key API_KEY env var")
			for _, e := range container.Env {
				Expect(e.Name).NotTo(Equal("KONVEYOR_LLM_API_KEY"))
			}

			By("exposing the whole credential Secret via envFrom, before user sources")
			Expect(container.EnvFrom).To(HaveLen(2))
			Expect(container.EnvFrom[0].SecretRef).NotTo(BeNil())
			Expect(container.EnvFrom[0].SecretRef.Name).To(Equal(secretName))
			Expect(container.EnvFrom[1].ConfigMapRef).NotTo(BeNil())
			Expect(container.EnvFrom[1].ConfigMapRef.Name).To(Equal("user-extra-env"))

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when the Sandbox finishes with a failed pod", func() {
		const (
			name       = "ar-ctrl-termination"
			agentName  = "ar-ctrl-agent-term"
			gwName     = "ar-prov-term"
			secretName = "ar-secret-term"
		)

		It("should surface the pod termination message on the Succeeded condition", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: agentName, Gateway: gwName},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			var fetchedRun konveyoriov1alpha1.AgentRun
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, runKey, &fetchedRun)).To(Succeed())
				g.Expect(fetchedRun.Status.SandboxName).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			By("simulating a failed agent pod with a termination message")
			const terminationMsg = `source repository "https://svn.example/repo" uses SCM kind "subversion"; only git is supported`
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-pod",
					Namespace: testNamespace,
					Labels:    map[string]string{labelAgentRun: name},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: agentContainerName, Image: testAgentImage}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
				Name: agentContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{Message: terminationMsg, ExitCode: 1},
				},
			}}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			By("marking the Sandbox Finished with a non-success reason")
			var sandbox sandboxv1beta1.Sandbox
			sandboxKey := types.NamespacedName{Name: fetchedRun.Status.SandboxName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, sandboxKey, &sandbox)).To(Succeed())
			sandbox.Status.Conditions = append(sandbox.Status.Conditions, metav1.Condition{
				Type:               sandboxConditionFinished,
				Status:             metav1.ConditionTrue,
				Reason:             "PodFailed",
				Message:            "pod failed",
				LastTransitionTime: metav1.Now(),
			})
			Expect(k8sClient.Status().Update(ctx, &sandbox)).To(Succeed())

			By("verifying the termination message is surfaced on the Succeeded condition")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, runKey, &fetchedRun)).To(Succeed())
				g.Expect(fetchedRun.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				cond := meta.FindStatusCondition(fetchedRun.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(konveyoriov1alpha1.AgentRunReasonFailed))
				g.Expect(cond.Message).To(Equal(terminationMsg))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when the run's pod is stuck on a fatal startup error", func() {
		const (
			name       = "ar-ctrl-imagepull"
			agentName  = "ar-ctrl-agent-imagepull"
			gwName     = "ar-prov-imagepull"
			secretName = "ar-secret-imagepull"
		)

		It("should fail the run with the kubelet waiting reason", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: agentName, Gateway: gwName},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			var fetchedRun konveyoriov1alpha1.AgentRun
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, runKey, &fetchedRun)).To(Succeed())
				g.Expect(fetchedRun.Status.SandboxName).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			By("simulating the sandbox pod with its init container in ImagePullBackOff")
			// The controller reads the pod by the Sandbox name, so the pod
			// must carry it (Agent Sandbox names the pod after the Sandbox).
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fetchedRun.Status.SandboxName,
					Namespace: testNamespace,
					Labels:    map[string]string{labelAgentRun: name},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:  corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{{Name: "skill-loader", Image: "bad/image:nope"}},
					Containers:     []corev1.Container{{Name: agentContainerName, Image: testAgentImage}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			pod.Status.Phase = corev1.PodPending
			pod.Status.InitContainerStatuses = []corev1.ContainerStatus{{
				Name: "skill-loader",
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{
						Reason:  "ImagePullBackOff",
						Message: `Back-off pulling image "bad/image:nope"`,
					},
				},
			}}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			By("verifying the run fails fast with reason ImagePullBackOff")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, runKey, &fetchedRun)).To(Succeed())
				g.Expect(fetchedRun.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				cond := meta.FindStatusCondition(fetchedRun.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("ImagePullBackOff"))
				g.Expect(cond.Message).To(ContainSubstring("skill-loader"))
				g.Expect(fetchedRun.Status.CompletionTime).NotTo(BeNil())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when the run's pod does not start before the startup deadline", func() {
		const (
			name       = "ar-ctrl-deadline"
			agentName  = "ar-ctrl-agent-deadline"
			gwName     = "ar-prov-deadline"
			secretName = "ar-secret-deadline"
		)

		It("should fail the run with StartupDeadlineExceeded", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			By("creating a run with a short per-run startup deadline")
			deadline := int32(2)
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef:               agentName,
					Gateway:                gwName,
					StartupDeadlineSeconds: &deadline,
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			runKey := types.NamespacedName{Name: name, Namespace: testNamespace}
			var fetchedRun konveyoriov1alpha1.AgentRun
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, runKey, &fetchedRun)).To(Succeed())
				g.Expect(fetchedRun.Status.SandboxName).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			By("simulating a pod that stays Pending (unschedulable)")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fetchedRun.Status.SandboxName,
					Namespace: testNamespace,
					Labels:    map[string]string{labelAgentRun: name},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: agentContainerName, Image: testAgentImage}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			pod.Status.Phase = corev1.PodPending
			pod.Status.Conditions = []corev1.PodCondition{{
				Type:    corev1.PodScheduled,
				Status:  corev1.ConditionFalse,
				Reason:  corev1.PodReasonUnschedulable,
				Message: "0/3 nodes are available",
			}}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			By("verifying the run fails once the deadline elapses")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, runKey, &fetchedRun)).To(Succeed())
				g.Expect(fetchedRun.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				cond := meta.FindStatusCondition(fetchedRun.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(konveyoriov1alpha1.AgentRunReasonStartupDeadlineExceeded))
				g.Expect(fetchedRun.Status.CompletionTime).NotTo(BeNil())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})
})
