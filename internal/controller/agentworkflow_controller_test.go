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

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

var _ = Describe("AgentWorkflow Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("when a stage references a non-existent Agent", func() {
		const (
			workflowName = "ap-ctrl-missing-agent"
		)

		It("should set Ready=False with AgentsNotReady", func() {
			workflow := &konveyoriov1alpha1.AgentWorkflow{
				ObjectMeta: metav1.ObjectMeta{Name: workflowName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowSpec{
					Stages: []konveyoriov1alpha1.AgentWorkflowStage{
						{Name: "plan", AgentRef: "nonexistent-agent"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workflow)).To(Succeed())

			key := types.NamespacedName{Name: workflowName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflow
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("AgentsNotReady"))
				g.Expect(readyCond.Message).To(ContainSubstring("nonexistent-agent"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, workflow)).To(Succeed())
		})
	})

	Context("when all stage Agents exist and are Ready", func() {
		const (
			workflowName = "ap-ctrl-all-ready"
			agentName1   = "ap-ctrl-agent-1"
			agentName2   = "ap-ctrl-agent-2"
			gwName       = "ap-prov-ready"
			secretName   = "ap-secret-ready"
		)

		It("should set Ready=True with AllAgentsReady", func() {
			cleanup := makeReadyGateway(gwName, secretName)
			defer cleanup()

			agent1 := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName1, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent1)).To(Succeed())
			waitForAgentReady(agentName1)

			agent2 := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: agentName2, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testAgentImage,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{{Ref: gwName}},
				},
			}
			Expect(k8sClient.Create(ctx, agent2)).To(Succeed())
			waitForAgentReady(agentName2)

			workflow := &konveyoriov1alpha1.AgentWorkflow{
				ObjectMeta: metav1.ObjectMeta{Name: workflowName, Namespace: testNamespace},
				Spec: konveyoriov1alpha1.AgentWorkflowSpec{
					Guide: "Test migration workflow",
					Stages: []konveyoriov1alpha1.AgentWorkflowStage{
						{Name: "plan", AgentRef: agentName1, Instructions: "Create a plan"},
						{Name: "execute", AgentRef: agentName2, Instructions: "Execute the plan"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workflow)).To(Succeed())

			key := types.NamespacedName{Name: workflowName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.AgentWorkflow
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal("AllAgentsReady"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, workflow)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent1)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent2)).To(Succeed())
		})
	})
})
