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

var _ = Describe("SkillCard Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("when reconciling a SkillCard with an image source", func() {
		const (
			name  = "sc-ctrl-image"
			image = "quay.io/konveyor/skills/maven-migration:1.0.0"
		)

		It("should set resolvedImage and Ready=True", func() {
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Image: image,
				},
			}
			Expect(k8sClient.Create(ctx, sc)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCard
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.ResolvedImage).To(Equal(image))

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCond.Reason).To(Equal("ImageResolved"))
				g.Expect(fetched.Status.ObservedGeneration).To(Equal(fetched.Generation))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, sc)).To(Succeed())
		})
	})

	Context("when reconciling a SkillCard with a source URL", func() {
		const name = "sc-ctrl-source"

		It("should set Ready=False with SourceNotSupported", func() {
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Source: "https://github.com/konveyor/skills/tree/main/maven",
				},
			}
			Expect(k8sClient.Create(ctx, sc)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCard
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.ResolvedImage).To(BeEmpty())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("SourceNotSupported"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, sc)).To(Succeed())
		})
	})

	Context("when reconciling a SkillCard with inline content", func() {
		const name = "sc-ctrl-inline"

		It("should set Ready=False with InlineNotSupported", func() {
			sc := &konveyoriov1alpha1.SkillCard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: konveyoriov1alpha1.SkillCardSpec{
					Inline: "# No javax\nDo not use javax packages.",
				},
			}
			Expect(k8sClient.Create(ctx, sc)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				var fetched konveyoriov1alpha1.SkillCard
				g.Expect(k8sClient.Get(ctx, key, &fetched)).To(Succeed())
				g.Expect(fetched.Status.ResolvedImage).To(BeEmpty())

				readyCond := meta.FindStatusCondition(fetched.Status.Conditions, ConditionTypeReady)
				g.Expect(readyCond).NotTo(BeNil())
				g.Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCond.Reason).To(Equal("InlineNotSupported"))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, sc)).To(Succeed())
		})
	})
})
