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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// updateAgentRunStatus fetches the latest AgentRun by name, applies the
// given mutation to its status, and retries on conflict. This avoids
// races with the controller reconciling between Get and Status().Update.
func updateAgentRunStatus(name string, mutate func(*konveyoriov1alpha1.AgentRun)) {
	EventuallyWithOffset(1, func(g Gomega) {
		var run konveyoriov1alpha1.AgentRun
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: name, Namespace: testNamespace,
		}, &run)).To(Succeed())
		mutate(&run)
		g.Expect(k8sClient.Status().Update(ctx, &run)).To(Succeed())
	}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
}

// waitForPlaybookReady waits until the named AgentPlaybook has Ready=True.
func waitForPlaybookReady(playbookName string) {
	key := types.NamespacedName{Name: playbookName, Namespace: testNamespace}
	EventuallyWithOffset(1, func(g Gomega) {
		var fetched konveyoriov1alpha1.AgentPlaybook
		g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
		readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
		g.Expect(readyCond).NotTo(BeNil())
		g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
	}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
}

var _ = Describe("AgentPlaybookRun Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("when the referenced AgentPlaybook does not exist", func() {
		const name = "apr-ctrl-no-playbook"

		It("should set Phase=Failed with PlaybookNotFound", func() {
			pbRun := &konveyoriov1alpha1.AgentPlaybookRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentPlaybookRunSpec{
					PlaybookRef: "nonexistent-playbook",
				},
			}
			Expect(k8sClient.Create(ctx, pbRun)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentPlaybookRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal("PlaybookNotFound"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, pbRun)).To(Succeed())
		})
	})

	Context("when the playbook is valid and stages execute sequentially", func() {
		const (
			playbookName = "apr-ctrl-seq-playbook"
			pbRunName    = "apr-ctrl-seq-run"
			agentName    = "apr-ctrl-seq-agent"
			provName     = "apr-prov-seq"
			secretName   = "apr-secret-seq"
		)

		It("should create AgentRuns per stage and advance on completion", func() {
			cleanup := makeReadyProvider(provName, secretName)
			defer cleanup()

			By("creating a Ready Agent")
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:     testAgentImage,
					Providers: []konveyoriov1alpha1.AgentProviderRef{{Ref: provName}},
					Params: []konveyoriov1alpha1.AgentParam{
						{Name: testParamName, Required: true},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			By("creating a Ready AgentPlaybook with two stages")
			playbook := &konveyoriov1alpha1.AgentPlaybook{
				ObjectMeta: metav1.ObjectMeta{Name: playbookName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentPlaybookSpec{
					Guide: "Sequential test playbook",
					Stages: []konveyoriov1alpha1.AgentPlaybookStage{
						{Name: "stage-a", AgentRef: agentName, Instructions: "Do stage A"},
						{Name: "stage-b", AgentRef: agentName, Instructions: "Do stage B"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, playbook)).To(Succeed())
			waitForPlaybookReady(playbookName)

			By("creating the AgentPlaybookRun")
			pbRun := &konveyoriov1alpha1.AgentPlaybookRun{
				ObjectMeta: metav1.ObjectMeta{Name: pbRunName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentPlaybookRunSpec{
					PlaybookRef: playbookName,
					Models: []konveyoriov1alpha1.AgentRunModelSelection{
						{Role: testRolePrimary, Provider: provName, Model: testLLMModelName},
					},
					Params: []konveyoriov1alpha1.AgentRunParam{
						{Name: testParamName, Value: testRepoURL},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pbRun)).To(Succeed())

			By("verifying stage-a AgentRun is created with deterministic name")
			pbRunKey := types.NamespacedName{Name: pbRunName, Namespace: testNamespace}
			expectedStageAName := stageAgentRunName(pbRunName, "stage-a")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentPlaybookRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseRunning))
				g.Expect(fetched.Status.CurrentStage).To(Equal("stage-a"))
				g.Expect(fetched.Status.Stages).To(HaveLen(2))
				g.Expect(fetched.Status.Stages[0].AgentRunName).To(Equal(expectedStageAName))
			}, timeout, interval).Should(Succeed())
			stageARunName := expectedStageAName

			By("verifying stage-a AgentRun has correct spec")
			var stageARun konveyoriov1alpha1.AgentRun
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: stageARunName, Namespace: testNamespace,
			}, &stageARun)).To(Succeed())
			Expect(stageARun.Spec.AgentRef).To(Equal(agentName))
			Expect(stageARun.Spec.Instructions).To(Equal("Do stage A"))
			Expect(stageARun.Spec.Params).To(HaveLen(1))
			Expect(stageARun.Spec.Params[0].Name).To(Equal(testParamName))
			Expect(stageARun.Spec.Params[0].Value).To(Equal(testRepoURL))
			Expect(stageARun.Spec.Models).To(HaveLen(1))
			Expect(stageARun.Spec.Models[0].Role).To(Equal(testRolePrimary))

			By("verifying stage-a AgentRun has correct labels")
			Expect(stageARun.Labels).To(HaveKeyWithValue(labelAgentPlaybookRun, pbRunName))
			Expect(stageARun.Labels).To(HaveKeyWithValue(labelStage, "stage-a"))

			By("verifying stage-b is not started yet")
			var fetchedPBRun konveyoriov1alpha1.AgentPlaybookRun
			Expect(k8sClient.Get(ctx, pbRunKey, &fetchedPBRun)).To(Succeed())
			Expect(fetchedPBRun.Status.Stages[1].AgentRunName).To(BeEmpty())
			Expect(fetchedPBRun.Status.Stages[1].Phase).To(Equal(konveyoriov1alpha1.AgentRunPhasePending))

			By("simulating stage-a AgentRun success")
			updateAgentRunStatus(stageARunName, func(run *konveyoriov1alpha1.AgentRun) {
				run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseSucceeded
				meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
					Type:   ConditionTypeReady,
					Status: metav1.ConditionTrue,
					Reason: reasonSucceeded,
				})
			})

			By("verifying stage-b AgentRun is created")
			var stageBRunName string
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentPlaybookRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.CurrentStage).To(Equal("stage-b"))
				g.Expect(fetched.Status.Stages[0].Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseSucceeded))
				g.Expect(fetched.Status.Stages[1].AgentRunName).NotTo(BeEmpty())
				stageBRunName = fetched.Status.Stages[1].AgentRunName
			}, timeout, interval).Should(Succeed())

			By("verifying stage-b AgentRun has correct spec")
			var stageBRun konveyoriov1alpha1.AgentRun
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: stageBRunName, Namespace: testNamespace,
			}, &stageBRun)).To(Succeed())
			Expect(stageBRun.Spec.Instructions).To(Equal("Do stage B"))
			Expect(stageBRun.Labels).To(HaveKeyWithValue(labelStage, "stage-b"))

			By("simulating stage-b AgentRun success")
			updateAgentRunStatus(stageBRunName, func(run *konveyoriov1alpha1.AgentRun) {
				run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseSucceeded
				meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
					Type:   ConditionTypeReady,
					Status: metav1.ConditionTrue,
					Reason: reasonSucceeded,
				})
			})

			By("verifying the playbook run completes successfully")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentPlaybookRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseSucceeded))
				g.Expect(fetched.Status.CompletionTime).NotTo(BeNil())
				g.Expect(fetched.Status.CurrentStage).To(BeEmpty())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal(reasonSucceeded))
			}, timeout, interval).Should(Succeed())

			By("cleaning up")
			var runList konveyoriov1alpha1.AgentRunList
			Expect(k8sClient.List(ctx, &runList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{labelAgentPlaybookRun: pbRunName},
			)).To(Succeed())
			for i := range runList.Items {
				Expect(k8sClient.Delete(ctx, &runList.Items[i])).To(Succeed())
			}
			Expect(k8sClient.Delete(ctx, pbRun)).To(Succeed())
			Expect(k8sClient.Delete(ctx, playbook)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when a stage fails", func() {
		const (
			playbookName = "apr-ctrl-fail-playbook"
			pbRunName    = "apr-ctrl-fail-run"
			agentName    = "apr-ctrl-fail-agent"
			provName     = "apr-prov-fail"
			secretName   = "apr-secret-fail"
		)

		It("should fail the entire playbook run", func() {
			cleanup := makeReadyProvider(provName, secretName)
			defer cleanup()

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:     testAgentImage,
					Providers: []konveyoriov1alpha1.AgentProviderRef{{Ref: provName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			playbook := &konveyoriov1alpha1.AgentPlaybook{
				ObjectMeta: metav1.ObjectMeta{Name: playbookName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentPlaybookSpec{
					Stages: []konveyoriov1alpha1.AgentPlaybookStage{
						{Name: "will-fail", AgentRef: agentName, Instructions: "This will fail"},
						{Name: "never-runs", AgentRef: agentName, Instructions: "Should not run"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, playbook)).To(Succeed())
			waitForPlaybookReady(playbookName)

			pbRun := &konveyoriov1alpha1.AgentPlaybookRun{
				ObjectMeta: metav1.ObjectMeta{Name: pbRunName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentPlaybookRunSpec{
					PlaybookRef: playbookName,
					Models: []konveyoriov1alpha1.AgentRunModelSelection{
						{Role: testRolePrimary, Provider: provName, Model: testLLMModelName},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pbRun)).To(Succeed())

			By("waiting for stage-1 AgentRun to be created")
			pbRunKey := types.NamespacedName{Name: pbRunName, Namespace: testNamespace}
			var stageRunName string
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentPlaybookRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Stages).To(HaveLen(2))
				g.Expect(fetched.Status.Stages[0].AgentRunName).NotTo(BeEmpty())
				stageRunName = fetched.Status.Stages[0].AgentRunName
			}, timeout, interval).Should(Succeed())

			By("simulating stage-1 failure")
			updateAgentRunStatus(stageRunName, func(run *konveyoriov1alpha1.AgentRun) {
				run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
				meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
					Type:   ConditionTypeReady,
					Status: metav1.ConditionFalse,
					Reason: "Failed",
				})
			})

			By("verifying the playbook run fails")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentPlaybookRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				g.Expect(fetched.Status.CompletionTime).NotTo(BeNil())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal("StageFailed"))
			}, timeout, interval).Should(Succeed())

			By("verifying stage-2 was never started")
			var finalPBRun konveyoriov1alpha1.AgentPlaybookRun
			Expect(k8sClient.Get(ctx, pbRunKey, &finalPBRun)).To(Succeed())
			Expect(finalPBRun.Status.Stages[1].AgentRunName).To(BeEmpty())
			Expect(finalPBRun.Status.Stages[1].Phase).To(Equal(konveyoriov1alpha1.AgentRunPhasePending))

			// Clean up — delete the AgentRuns owned by the playbook run
			// first to avoid GC issues in tests.
			var runList konveyoriov1alpha1.AgentRunList
			Expect(k8sClient.List(ctx, &runList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{labelAgentPlaybookRun: pbRunName},
			)).To(Succeed())
			for i := range runList.Items {
				Expect(k8sClient.Delete(ctx, &runList.Items[i])).To(Succeed())
			}
			Expect(k8sClient.Delete(ctx, pbRun)).To(Succeed())
			Expect(k8sClient.Delete(ctx, playbook)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})
})
