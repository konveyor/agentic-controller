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

// waitForWorkflowReady waits until the named AgentWorkflow has Ready=True.
func waitForWorkflowReady(workflowName string) {
	key := types.NamespacedName{Name: workflowName, Namespace: testNamespace}
	EventuallyWithOffset(1, func(g Gomega) {
		var fetched konveyoriov1alpha1.AgentWorkflow
		g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
		readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
		g.Expect(readyCond).NotTo(BeNil())
		g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
	}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
}

var _ = Describe("AgentWorkflowRun Controller", func() {
	const (
		timeout    = 10 * time.Second
		interval   = 250 * time.Millisecond
		stageAName = "stage-a"
	)

	Context("when the referenced AgentWorkflow does not exist", func() {
		const name = "apr-ctrl-no-workflow"

		It("should set Phase=Failed with WorkflowNotFound", func() {
			pbRun := &konveyoriov1alpha1.AgentWorkflowRun{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowRunSpec{
					WorkflowRef: "nonexistent-workflow",
				},
			}
			Expect(k8sClient.Create(ctx, pbRun)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflowRun
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal("WorkflowNotFound"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, pbRun)).To(Succeed())
		})
	})

	Context("when the workflow is valid and stages execute sequentially", func() {
		const (
			workflowName = "apr-ctrl-seq-workflow"
			pbRunName    = "apr-ctrl-seq-run"
			agentName    = "apr-ctrl-seq-agent"
			gwName       = "apr-prov-seq"
			secretName   = "apr-secret-seq"
		)

		It("should create AgentRuns per stage and advance on completion", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			By("creating a Ready Agent")
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
					Params: []konveyoriov1alpha1.AgentParam{
						{Name: testParamName, Required: true},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			waitForAgentReady(agentName)

			By("creating a Ready AgentWorkflow with two stages")
			workflow := &konveyoriov1alpha1.AgentWorkflow{
				ObjectMeta: metav1.ObjectMeta{Name: workflowName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowSpec{
					Guide: "Sequential test workflow",
					Stages: []konveyoriov1alpha1.AgentWorkflowStage{
						{Name: stageAName, AgentRef: agentName, Instructions: "Do stage A"},
						{Name: "stage-b", AgentRef: agentName, Instructions: "Do stage B"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workflow)).To(Succeed())
			waitForWorkflowReady(workflowName)

			By("creating the AgentWorkflowRun")
			pbRun := &konveyoriov1alpha1.AgentWorkflowRun{
				ObjectMeta: metav1.ObjectMeta{Name: pbRunName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowRunSpec{
					WorkflowRef: workflowName,
					Gateway:     gwName,
					Params: []konveyoriov1alpha1.AgentRunParam{
						{Name: testParamName, Value: testRepoURL},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pbRun)).To(Succeed())

			By("verifying stage-a AgentRun is created with deterministic name")
			pbRunKey := types.NamespacedName{Name: pbRunName, Namespace: testNamespace}
			expectedStageAName := stageAgentRunName(pbRunName, stageAName)
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflowRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseRunning))
				g.Expect(fetched.Status.CurrentStage).To(Equal(stageAName))
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
			Expect(stageARun.Spec.Gateway).To(Equal(gwName))

			By("verifying stage-a AgentRun has correct labels")
			Expect(stageARun.Labels).To(HaveKeyWithValue(labelAgentWorkflowRun, pbRunName))
			Expect(stageARun.Labels).To(HaveKeyWithValue(labelStage, stageAName))

			By("verifying stage-b is not started yet")
			var fetchedPBRun konveyoriov1alpha1.AgentWorkflowRun
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
				var fetched konveyoriov1alpha1.AgentWorkflowRun
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

			By("verifying the workflow run completes successfully")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflowRun
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
				client.MatchingLabels{labelAgentWorkflowRun: pbRunName},
			)).To(Succeed())
			for i := range runList.Items {
				Expect(k8sClient.Delete(ctx, &runList.Items[i])).To(Succeed())
			}
			Expect(k8sClient.Delete(ctx, pbRun)).To(Succeed())
			Expect(k8sClient.Delete(ctx, workflow)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when stages use different Agents with different params", func() {
		const (
			workflowName = "apr-ctrl-filter-workflow"
			pbRunName    = "apr-ctrl-filter-run"
			agentAName   = "apr-ctrl-filter-agent-a"
			agentBName   = "apr-ctrl-filter-agent-b"
			gwName       = "apr-prov-filter"
			secretName   = "apr-secret-filter"
		)

		It("should forward only params each stage Agent declares", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			By("creating Agent A that declares 'source_url' only")
			agentA := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentAName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
					Params: []konveyoriov1alpha1.AgentParam{
						{Name: "source_url", Required: true},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agentA)).To(Succeed())
			waitForAgentReady(agentAName)

			By("creating Agent B that declares 'target_branch' only")
			agentB := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentBName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
					Params: []konveyoriov1alpha1.AgentParam{
						{Name: testParamTargetBranch, Required: true},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agentB)).To(Succeed())
			waitForAgentReady(agentBName)

			By("creating a workflow with two stages using different Agents")
			workflow := &konveyoriov1alpha1.AgentWorkflow{
				ObjectMeta: metav1.ObjectMeta{Name: workflowName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowSpec{
					Stages: []konveyoriov1alpha1.AgentWorkflowStage{
						{Name: stageAName, AgentRef: agentAName},
						{Name: "stage-b", AgentRef: agentBName},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workflow)).To(Succeed())
			waitForWorkflowReady(workflowName)

			By("creating the workflow run with params for both stages")
			pbRun := &konveyoriov1alpha1.AgentWorkflowRun{
				ObjectMeta: metav1.ObjectMeta{Name: pbRunName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowRunSpec{
					WorkflowRef: workflowName,
					Gateway:     gwName,
					Params: []konveyoriov1alpha1.AgentRunParam{
						{Name: "source_url", Value: "https://github.com/example/repo.git"},
						{Name: testParamTargetBranch, Value: "konveyor/test"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pbRun)).To(Succeed())

			By("verifying stage-a AgentRun gets only 'source_url'")
			pbRunKey := types.NamespacedName{Name: pbRunName, Namespace: testNamespace}
			expectedStageAName := stageAgentRunName(pbRunName, stageAName)
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflowRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Stages).To(HaveLen(2))
				g.Expect(fetched.Status.Stages[0].AgentRunName).To(Equal(expectedStageAName))
			}, timeout, interval).Should(Succeed())

			var stageARun konveyoriov1alpha1.AgentRun
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: expectedStageAName, Namespace: testNamespace,
			}, &stageARun)).To(Succeed())
			Expect(stageARun.Spec.Params).To(HaveLen(1))
			Expect(stageARun.Spec.Params[0].Name).To(Equal("source_url"))

			By("simulating stage-a success to advance to stage-b")
			updateAgentRunStatus(expectedStageAName, func(run *konveyoriov1alpha1.AgentRun) {
				run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseSucceeded
				now := metav1.Now()
				run.Status.CompletionTime = &now
				meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
					Type:   ConditionTypeReady,
					Status: metav1.ConditionTrue,
					Reason: reasonSucceeded,
				})
			})

			By("verifying stage-b AgentRun gets only 'target_branch'")
			expectedStageBName := stageAgentRunName(pbRunName, "stage-b")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflowRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Stages).To(HaveLen(2))
				g.Expect(fetched.Status.Stages[1].AgentRunName).To(Equal(expectedStageBName))
			}, timeout, interval).Should(Succeed())

			var stageBRun konveyoriov1alpha1.AgentRun
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: expectedStageBName, Namespace: testNamespace,
			}, &stageBRun)).To(Succeed())
			Expect(stageBRun.Spec.Params).To(HaveLen(1))
			Expect(stageBRun.Spec.Params[0].Name).To(Equal(testParamTargetBranch))

			By("cleaning up")
			var runList konveyoriov1alpha1.AgentRunList
			Expect(k8sClient.List(ctx, &runList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{labelAgentWorkflowRun: pbRunName},
			)).To(Succeed())
			for i := range runList.Items {
				Expect(k8sClient.Delete(ctx, &runList.Items[i])).To(Succeed())
			}
			Expect(k8sClient.Delete(ctx, pbRun)).To(Succeed())
			Expect(k8sClient.Delete(ctx, workflow)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agentA)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agentB)).To(Succeed())
		})
	})

	Context("when the workflow run carries caller-supplied labels", func() {
		const (
			workflowName = "apr-ctrl-labels-workflow"
			pbRunName    = "apr-ctrl-labels-run"
			agentName    = "apr-ctrl-labels-agent"
			gwName       = "apr-prov-labels"
			secretName   = "apr-secret-labels"
		)

		It("should propagate parent labels to stage AgentRuns with controller-owned keys winning", func() {
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

			workflow := &konveyoriov1alpha1.AgentWorkflow{
				ObjectMeta: metav1.ObjectMeta{Name: workflowName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowSpec{
					Stages: []konveyoriov1alpha1.AgentWorkflowStage{
						{Name: stageAName, AgentRef: agentName, Instructions: "Do stage A"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workflow)).To(Succeed())
			waitForWorkflowReady(workflowName)

			By("creating the workflow run with caller labels and spoofed controller-owned keys")
			pbRun := &konveyoriov1alpha1.AgentWorkflowRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pbRunName,
					Namespace: testNamespace,
					Labels: map[string]string{
						"konveyor.io/application": "42",
						"custom/foo":              "bar",
						labelManagedBy:            "spoofed-manager",
						labelAgentWorkflowRun:     "spoofed-run",
						labelStage:                "spoofed-stage",
					},
				},
				Spec: konveyoriov1alpha1.AgentWorkflowRunSpec{
					WorkflowRef: workflowName,
					Gateway:     gwName,
				},
			}
			Expect(k8sClient.Create(ctx, pbRun)).To(Succeed())

			By("waiting for the stage AgentRun to be created")
			pbRunKey := types.NamespacedName{Name: pbRunName, Namespace: testNamespace}
			expectedStageName := stageAgentRunName(pbRunName, stageAName)
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflowRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Stages).To(HaveLen(1))
				g.Expect(fetched.Status.Stages[0].AgentRunName).To(Equal(expectedStageName))
			}, timeout, interval).Should(Succeed())

			By("verifying the stage AgentRun inherits caller labels")
			var stageRun konveyoriov1alpha1.AgentRun
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: expectedStageName, Namespace: testNamespace,
			}, &stageRun)).To(Succeed())
			Expect(stageRun.Labels).To(HaveKeyWithValue("konveyor.io/application", "42"))
			Expect(stageRun.Labels).To(HaveKeyWithValue("custom/foo", "bar"))

			By("verifying controller-owned keys keep controller values")
			Expect(stageRun.Labels).To(HaveKeyWithValue(labelManagedBy, managedByLabel))
			Expect(stageRun.Labels).To(HaveKeyWithValue(labelAgentWorkflowRun, pbRunName))
			Expect(stageRun.Labels).To(HaveKeyWithValue(labelStage, stageAName))

			By("cleaning up")
			var runList konveyoriov1alpha1.AgentRunList
			Expect(k8sClient.List(ctx, &runList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{labelAgentWorkflowRun: pbRunName},
			)).To(Succeed())
			for i := range runList.Items {
				Expect(k8sClient.Delete(ctx, &runList.Items[i])).To(Succeed())
			}
			Expect(k8sClient.Delete(ctx, pbRun)).To(Succeed())
			Expect(k8sClient.Delete(ctx, workflow)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})

	Context("when a stage fails", func() {
		const (
			workflowName = "apr-ctrl-fail-workflow"
			pbRunName    = "apr-ctrl-fail-run"
			agentName    = "apr-ctrl-fail-agent"
			gwName       = "apr-prov-fail"
			secretName   = "apr-secret-fail"
		)

		It("should fail the entire workflow run", func() {
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

			workflow := &konveyoriov1alpha1.AgentWorkflow{
				ObjectMeta: metav1.ObjectMeta{Name: workflowName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowSpec{
					Stages: []konveyoriov1alpha1.AgentWorkflowStage{
						{Name: "will-fail", AgentRef: agentName, Instructions: "This will fail"},
						{Name: "never-runs", AgentRef: agentName, Instructions: "Should not run"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workflow)).To(Succeed())
			waitForWorkflowReady(workflowName)

			pbRun := &konveyoriov1alpha1.AgentWorkflowRun{
				ObjectMeta: metav1.ObjectMeta{Name: pbRunName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowRunSpec{
					WorkflowRef: workflowName,
					Gateway:     gwName,
				},
			}
			Expect(k8sClient.Create(ctx, pbRun)).To(Succeed())

			By("waiting for stage-1 AgentRun to be created")
			pbRunKey := types.NamespacedName{Name: pbRunName, Namespace: testNamespace}
			var stageRunName string
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflowRun
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

			By("verifying the workflow run fails")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflowRun
				g.Expect(k8sClient.Get(ctx, pbRunKey, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(konveyoriov1alpha1.AgentRunPhaseFailed))
				g.Expect(fetched.Status.CompletionTime).NotTo(BeNil())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Reason).To(Equal("StageFailed"))
			}, timeout, interval).Should(Succeed())

			By("verifying stage-2 was never started")
			var finalPBRun konveyoriov1alpha1.AgentWorkflowRun
			Expect(k8sClient.Get(ctx, pbRunKey, &finalPBRun)).To(Succeed())
			Expect(finalPBRun.Status.Stages[1].AgentRunName).To(BeEmpty())
			Expect(finalPBRun.Status.Stages[1].Phase).To(Equal(konveyoriov1alpha1.AgentRunPhasePending))

			// Clean up — delete the AgentRuns owned by the workflow run
			// first to avoid GC issues in tests.
			var runList konveyoriov1alpha1.AgentRunList
			Expect(k8sClient.List(ctx, &runList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{labelAgentWorkflowRun: pbRunName},
			)).To(Succeed())
			for i := range runList.Items {
				Expect(k8sClient.Delete(ctx, &runList.Items[i])).To(Succeed())
			}
			Expect(k8sClient.Delete(ctx, pbRun)).To(Succeed())
			Expect(k8sClient.Delete(ctx, workflow)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})
	})
})
