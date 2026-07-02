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

var _ = Describe("SkillCollection Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("when all skills are image-based", func() {
		const collName = "scol-ctrl-all-images"

		It("should be Ready immediately", func() {
			scol := &konveyoriov1alpha1.SkillCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCollectionSpec{
					Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
						{Name: "maven-skill", Image: "quay.io/konveyor/skills/maven:1.0.0"},
						{Name: "javax-imports", Image: "quay.io/konveyor/skills/javax:1.0.0"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scol)).To(Succeed())

			key := types.NamespacedName{Name: collName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCollection
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal("AllSkillsResolved"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, scol)).To(Succeed())
		})
	})

	Context("when a skill references a missing SkillCard", func() {
		const collName = "scol-ctrl-missing-ref"

		It("should be NotReady", func() {
			scol := &konveyoriov1alpha1.SkillCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCollectionSpec{
					Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
						{Name: "missing-skill", SkillCardRef: "nonexistent-skillcard"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scol)).To(Succeed())

			key := types.NamespacedName{Name: collName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCollection
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("SkillsNotReady"))
				g.Expect(readyCond.Message).To(ContainSubstring("not found"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, scol)).To(Succeed())
		})
	})

	Context("when a SkillCard becomes Ready after the collection is created", func() {
		const (
			collName = "scol-ctrl-watch"
			scName   = "sc-ctrl-watched"
			scImage  = "quay.io/konveyor/skills/watched:1.0.0"
		)

		It("should transition from NotReady to Ready", func() {
			By("creating the SkillCollection referencing a SkillCard that does not exist yet")
			scol := &konveyoriov1alpha1.SkillCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCollectionSpec{
					Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
						{Name: "watched-skill", SkillCardRef: scName},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scol)).To(Succeed())

			By("verifying the collection is initially NotReady")
			collKey := types.NamespacedName{Name: collName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCollection
				g.Expect(k8sClient.Get(ctx, collKey, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			}, timeout, interval).Should(Succeed())

			By("creating the referenced SkillCard with an image source")
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Image: scImage,
				},
			}
			Expect(k8sClient.Create(ctx, sc)).To(Succeed())

			By("verifying the collection becomes Ready")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCollection
				g.Expect(k8sClient.Get(ctx, collKey, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal("AllSkillsResolved"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, scol)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sc)).To(Succeed())
		})
	})

	Context("when a skill uses a git source", func() {
		const collName = "scol-ctrl-git-source"

		It("should be NotReady with Phase 3 message", func() {
			scol := &konveyoriov1alpha1.SkillCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCollectionSpec{
					Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
						{Name: "git-skill", Source: "https://github.com/konveyor/skills/tree/main/test"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scol)).To(Succeed())

			key := types.NamespacedName{Name: collName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCollection
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Message).To(ContainSubstring("Phase 3"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, scol)).To(Succeed())
		})
	})

	Context("when a collection has mixed ready and not-ready skills", func() {
		const collName = "scol-ctrl-mixed"

		It("should report the correct count and all failure reasons", func() {
			scol := &konveyoriov1alpha1.SkillCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCollectionSpec{
					Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
						{Name: "image-skill", Image: "quay.io/konveyor/skills:test"},
						{Name: "missing-a", SkillCardRef: "does-not-exist-a"},
						{Name: "missing-b", SkillCardRef: "does-not-exist-b"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scol)).To(Succeed())

			key := types.NamespacedName{Name: collName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCollection
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Message).To(ContainSubstring("1 of 3"))
				g.Expect(readyCond.Message).To(ContainSubstring("does-not-exist-a"))
				g.Expect(readyCond.Message).To(ContainSubstring("does-not-exist-b"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, scol)).To(Succeed())
		})
	})

	Context("when a referenced SkillCard is deleted", func() {
		const (
			collName = "scol-ctrl-delete"
			scName   = "sc-ctrl-to-delete"
			scImage  = "quay.io/konveyor/skills:deleteme"
		)

		It("should transition from Ready to NotReady", func() {
			By("creating the SkillCard first")
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Image: scImage,
				},
			}
			Expect(k8sClient.Create(ctx, sc)).To(Succeed())

			scKey := types.NamespacedName{Name: scName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCard
				g.Expect(k8sClient.Get(ctx, scKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())

			By("creating the SkillCollection referencing it")
			scol := &konveyoriov1alpha1.SkillCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collName,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCollectionSpec{
					Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
						{Name: "deletable-skill", SkillCardRef: scName},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scol)).To(Succeed())

			collKey := types.NamespacedName{Name: collName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCollection
				g.Expect(k8sClient.Get(ctx, collKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())

			By("deleting the SkillCard")
			Expect(k8sClient.Delete(ctx, sc)).To(Succeed())

			By("verifying the collection becomes NotReady")
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCollection
				g.Expect(k8sClient.Get(ctx, collKey, &fetched)).To(Succeed())
				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Message).To(ContainSubstring("not found"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, scol)).To(Succeed())
		})
	})
})
