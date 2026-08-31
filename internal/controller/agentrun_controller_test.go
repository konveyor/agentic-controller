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
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

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
	jobKey := types.NamespacedName{Name: fmt.Sprintf("%s%s-gen1", verificationJobPrefix, gwName), Namespace: testNamespace}
	EventuallyWithOffset(1, func(g Gomega) {
		var job batchv1.Job
		g.Expect(k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
	}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

	var job batchv1.Job
	ExpectWithOffset(1, k8sClient.Get(ctx, jobKey, &job)).To(Succeed())
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
				Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: "nonexistent-agent"},
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
				Spec:       konveyoriov1alpha1.SkillCardSpec{Image: "quay.io/konveyor/skills:test-skill"},
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
			Expect(sandbox.Spec.PodTemplate.ObjectMeta.Labels).To(HaveKeyWithValue("konveyor.io/agentrun", name))
			Expect(sandbox.Spec.PodTemplate.ObjectMeta.Labels).To(HaveKeyWithValue("konveyor.io/agent", agentName))

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

	Context("when the run declares fileMounts", func() {
		const (
			name       = "ar-ctrl-filemounts"
			agentName  = "ar-ctrl-agent-filemounts"
			gwName     = "ar-prov-filemounts"
			secretName = "ar-secret-filemounts"
		)

		It("should mount the Secret and ConfigMap read-only on the agent container", func() {
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
					FileMounts: []konveyoriov1alpha1.FileMount{
						{SecretName: testMountSecretName, MountPath: testMountSecretPath, SubPath: testMountKey},
						{ConfigMapName: testMountCMName, MountPath: testMountCMPath},
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
			podSpec := sandbox.Spec.PodTemplate.Spec
			container := podSpec.Containers[0]

			By("adding a read-only VolumeMount for each fileMount")
			var secretMount, cmMount *corev1.VolumeMount
			for i := range container.VolumeMounts {
				vm := &container.VolumeMounts[i]
				switch vm.MountPath {
				case testMountSecretPath:
					secretMount = vm
				case testMountCMPath:
					cmMount = vm
				}
			}
			Expect(secretMount).NotTo(BeNil())
			Expect(secretMount.ReadOnly).To(BeTrue())
			Expect(secretMount.SubPath).To(Equal(testMountKey))
			Expect(cmMount).NotTo(BeNil())
			Expect(cmMount.ReadOnly).To(BeTrue())
			Expect(cmMount.SubPath).To(BeEmpty())

			By("backing each mount with the right Secret/ConfigMap volume")
			vols := map[string]corev1.Volume{}
			for _, v := range podSpec.Volumes {
				vols[v.Name] = v
			}
			secretVol, ok := vols[secretMount.Name]
			Expect(ok).To(BeTrue())
			Expect(secretVol.Secret).NotTo(BeNil())
			Expect(secretVol.Secret.SecretName).To(Equal(testMountSecretName))
			cmVol, ok := vols[cmMount.Name]
			Expect(ok).To(BeTrue())
			Expect(cmVol.ConfigMap).NotTo(BeNil())
			Expect(cmVol.ConfigMap.Name).To(Equal(testMountCMName))

			Expect(k8sClient.Delete(ctx, run)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})

		It("should fail the run terminally when a mountPath collides with a reserved mount", func() {
			cleanup := makeReadyGatewayKeyless(gwName+"-collide", secretName+"-collide")
			defer cleanup()

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName + "-collide", Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName + "-collide"}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName + "-collide")

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-collide", Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: agentName + "-collide",
					Gateway:  gwName + "-collide",
					FileMounts: []konveyoriov1alpha1.FileMount{
						{SecretName: "shadow", MountPath: "/run/konveyor/params.json"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, run)).To(Succeed())

			runKey := types.NamespacedName{Name: name + "-collide", Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentRun
				g.Expect(k8sClient.Get(ctx, runKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				g.Expect(fetched.Status.SandboxName).To(BeEmpty())
				cond := meta.FindStatusCondition(fetched.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("InvalidFileMounts"))
			}, timeout, interval).Should(Succeed())

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
})
