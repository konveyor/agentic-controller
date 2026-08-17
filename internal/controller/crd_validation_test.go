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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	testImageGoose        = "quay.io/konveyor/agent-java-goose:latest"
	testParamName         = "source_url"
	testParamTargetBranch = "target_branch"
	testGateway           = "anthropic-gateway"
	testModel             = "claude-sonnet-4-20250514"
)

var _ = Describe("CRD Validation", func() {

	// ── SkillCard ──────────────────────────────────────────────────────
	Context("SkillCard", func() {
		It("should accept a SkillCard with an image source", func() {
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sc-image-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Image:       "quay.io/konveyor/skills/maven-migration:1.0.0",
					DisplayName: "Maven Migration",
					Version:     "1.0.0",
					Description: "Migrates Maven POM files.",
					Type:        konveyoriov1alpha1.SkillCardTypeSkill,
					Tags:        []string{"java", "maven"},
				},
			}
			Expect(k8sClient.Create(ctx, sc)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sc)).To(Succeed())
		})

		It("should accept a SkillCard with a source URL", func() {
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sc-source-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Source: "https://github.com/konveyor/skills/tree/main/maven",
					Type:   konveyoriov1alpha1.SkillCardTypeRule,
				},
			}
			Expect(k8sClient.Create(ctx, sc)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sc)).To(Succeed())
		})

		It("should accept a SkillCard with inline content", func() {
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sc-inline-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Inline: "# No javax imports\nDo not use javax packages.",
				},
			}
			Expect(k8sClient.Create(ctx, sc)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sc)).To(Succeed())
		})

		It("should reject a SkillCard with no source set", func() {
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sc-no-source-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					DisplayName: "Bad skill",
				},
			}
			err := k8sClient.Create(ctx, sc)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})

		It("should reject a SkillCard with multiple sources set", func() {
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sc-multi-source-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Image:  "quay.io/konveyor/skills/test:1.0.0",
					Inline: "# Test",
				},
			}
			err := k8sClient.Create(ctx, sc)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})
	})

	// ── SkillCollection ────────────────────────────────────────────────
	Context("SkillCollection", func() {
		It("should accept a SkillCollection with valid skill refs", func() {
			scol := &konveyoriov1alpha1.SkillCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scol-valid-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCollectionSpec{
					Version: "1.0.0",
					Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
						{Name: "maven-skill", SkillCardRef: "maven-skill-ref"},
						{Name: "javax-imports", Image: "quay.io/konveyor/skills/javax:1.0.0"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scol)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scol)).To(Succeed())
		})

		It("should reject a SkillCollection skill ref with multiple sources", func() {
			scol := &konveyoriov1alpha1.SkillCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scol-multi-source-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCollectionSpec{
					Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
						{
							Name:         "bad-skill",
							SkillCardRef: "some-ref",
							Image:        "quay.io/konveyor/skills/test:1.0.0",
						},
					},
				},
			}
			err := k8sClient.Create(ctx, scol)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})
	})

	// ── Gateway ───────────────────────────────────────────────────────
	Context("Gateway", func() {
		It("should accept a valid Gateway", func() {
			gw := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gw-valid-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: "anthropic",
					Endpoint: "https://api.anthropic.com",
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: "anthropic-credentials",
						Key:        "api-key",
					},
					Model: konveyoriov1alpha1.GatewayModel{
						Name:          testModel,
						ContextWindow: 200000,
						Tier:          "premium",
					},
				},
			}
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			Expect(k8sClient.Delete(ctx, gw)).To(Succeed())
		})
	})

	// ── Agent ──────────────────────────────────────────────────────────
	Context("Agent", func() {
		It("should accept a valid Agent", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-valid-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:  testImageGoose,
					Prompt: "You are a Java migration specialist.",
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{
						{Ref: testGateway},
					},
					Params: []konveyoriov1alpha1.AgentParam{
						{
							Name:        testParamName,
							Type:        konveyoriov1alpha1.AgentParamTypeString,
							Description: "Git URL of the application source",
							Required:    true,
						},
						{
							Name:    "source_branch",
							Type:    konveyoriov1alpha1.AgentParamTypeString,
							Default: testDefaultBranch,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})

		It("should accept a param that omits the required field entirely", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-omit-required-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image: testImageGoose,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{
						{Ref: testGatewayName},
					},
					Params: []konveyoriov1alpha1.AgentParam{
						{
							Name:    testParamTargetBranch,
							Default: testDefaultBranch,
							// required is omitted — this must not fail
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
		})

		It("should reject an Agent with required=true and a default value", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-bad-param-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image: testImageGoose,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{
						{Ref: testGatewayName},
					},
					Params: []konveyoriov1alpha1.AgentParam{
						{
							Name:     testParamName,
							Default:  "https://example.com",
							Required: true,
						},
					},
				},
			}
			err := k8sClient.Create(ctx, agent)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})

		It("should reject an Agent with no gateways", func() {
			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-no-gateways-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentSpec{
					Image:    testImageGoose,
					Gateways: []konveyoriov1alpha1.AgentGatewayRef{},
				},
			}
			err := k8sClient.Create(ctx, agent)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})
	})

	// ── AgentRun ───────────────────────────────────────────────────────
	Context("AgentRun", func() {
		It("should accept a valid AgentRun", func() {
			ar := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ar-valid-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: "java-migration-agent",
					Gateway:  testGateway,
					Params: []konveyoriov1alpha1.AgentRunParam{
						{Name: testParamName, Value: "https://github.com/acme/app.git"},
					},
					Instructions: "Migrate this application.",
					Env: []corev1.EnvVar{
						{Name: "HUB_BASE_URL", Value: "https://hub.konveyor.svc"},
					},
					EnvFrom: []corev1.EnvFromSource{
						{SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "hub-token"},
						}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ar)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ar)).To(Succeed())
		})

		It("should accept a minimal AgentRun", func() {
			ar := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ar-minimal-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: "some-agent",
				},
			}
			Expect(k8sClient.Create(ctx, ar)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ar)).To(Succeed())
		})

		It("should reject an AgentRun with empty agentRef", func() {
			ar := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ar-empty-ref-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: "",
				},
			}
			err := k8sClient.Create(ctx, ar)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})

		It("should reject mutation of agentRef", func() {
			ar := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ar-immutable-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentRunSpec{
					AgentRef: "original-agent",
				},
			}
			Expect(k8sClient.Create(ctx, ar)).To(Succeed())

			// Re-Get then Update in a retry loop: the reconciler may
			// patch status between our Get and Update, bumping the
			// resourceVersion and causing a conflict. Retrying ensures
			// we eventually hit the webhook validation error.
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ar), ar); err != nil {
					return false
				}
				ar.Spec.AgentRef = "different-agent"
				err := k8sClient.Update(ctx, ar)
				return err != nil && errors.IsInvalid(err)
			}).Should(BeTrue(), "expected Invalid error on agentRef mutation")

			// Clean up
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ar), ar)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ar)).To(Succeed())
		})
	})

	// ── AgentWorkflow ──────────────────────────────────────────────────
	Context("AgentWorkflow", func() {
		It("should accept a valid AgentWorkflow", func() {
			ap := &konveyoriov1alpha1.AgentWorkflow{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ap-valid-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentWorkflowSpec{
					Guide: "Migrate a Java EE application to Quarkus.",
					Stages: []konveyoriov1alpha1.AgentWorkflowStage{
						{Name: "discover", AgentRef: "discovery-agent", Instructions: "Analyze the app."},
						{Name: "implement", AgentRef: "migration-agent", Instructions: "Execute migration."},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ap)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ap)).To(Succeed())
		})

		It("should reject an AgentWorkflow with empty stages", func() {
			ap := &konveyoriov1alpha1.AgentWorkflow{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ap-empty-stages-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentWorkflowSpec{
					Guide:  "No stages here.",
					Stages: []konveyoriov1alpha1.AgentWorkflowStage{},
				},
			}
			err := k8sClient.Create(ctx, ap)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})
	})

	// ── Gateway negative tests ─────────────────────────────────────────
	Context("Gateway negative", func() {
		It("should reject a Gateway with empty endpoint", func() {
			gw := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gw-empty-endpoint-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: testProviderType,
					Endpoint: "",
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: "creds",
						Key:        "key",
					},
					Model: konveyoriov1alpha1.GatewayModel{
						Name: "model", ContextWindow: 100000,
					},
				},
			}
			err := k8sClient.Create(ctx, gw)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})

		It("should reject a Gateway with empty model name", func() {
			gw := &konveyoriov1alpha1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gw-empty-model-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.GatewaySpec{
					Provider: testProviderType,
					Endpoint: "https://api.example.com",
					CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{
						SecretName: "creds",
						Key:        "key",
					},
					Model: konveyoriov1alpha1.GatewayModel{
						Name: "", ContextWindow: 100000,
					},
				},
			}
			err := k8sClient.Create(ctx, gw)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), fmt.Sprintf("expected Invalid error, got: %v", err))
		})
	})

	// ── AgentWorkflowRun ───────────────────────────────────────────────
	Context("AgentWorkflowRun", func() {
		It("should accept a valid AgentWorkflowRun", func() {
			apr := &konveyoriov1alpha1.AgentWorkflowRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "apr-valid-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentWorkflowRunSpec{
					WorkflowRef: "java-migration",
					Gateway:     testGateway,
					Params: []konveyoriov1alpha1.AgentRunParam{
						{Name: testParamName, Value: "https://github.com/acme/app.git"},
					},
					Env: []corev1.EnvVar{
						{Name: "APP_ID", Value: "123"},
					},
					EnvFrom: []corev1.EnvFromSource{
						{SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "git-creds"},
						}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, apr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, apr)).To(Succeed())
		})

		It("should reject mutation of workflowRef", func() {
			apr := &konveyoriov1alpha1.AgentWorkflowRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "apr-immutable-test",
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.AgentWorkflowRunSpec{
					WorkflowRef: "original-workflow",
				},
			}
			Expect(k8sClient.Create(ctx, apr)).To(Succeed())

			// Re-Get then Update in a retry loop: the reconciler may
			// patch status between our Get and Update, bumping the
			// resourceVersion and causing a conflict. Retrying ensures
			// we eventually hit the webhook validation error.
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(apr), apr); err != nil {
					return false
				}
				apr.Spec.WorkflowRef = "different-workflow"
				err := k8sClient.Update(ctx, apr)
				return err != nil && errors.IsInvalid(err)
			}).Should(BeTrue(), "expected Invalid error on workflowRef mutation")

			// Clean up
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(apr), apr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, apr)).To(Succeed())
		})
	})
})
