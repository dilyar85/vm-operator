// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package mutation_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha4"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

func intgTests() {
	Describe(
		"Mutate",
		Label(
			testlabels.Create,
			testlabels.Update,
			testlabels.Delete,
			testlabels.EnvTest,
			testlabels.API,
			testlabels.Mutation,
			testlabels.Webhook,
		),
		intgTestsMutating,
	)
}

type intgMutatingWebhookContext struct {
	builder.IntegrationTestContext
	vmGroup *vmopv1.VirtualMachineGroup
}

func newIntgMutatingWebhookContext() *intgMutatingWebhookContext {
	ctx := &intgMutatingWebhookContext{
		IntegrationTestContext: *suite.NewIntegrationTestContext(),
	}

	ctx.vmGroup = &vmopv1.VirtualMachineGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy-vm-group",
			Namespace: ctx.Namespace,
		},
	}

	return ctx
}

func intgTestsMutating() {
	var (
		ctx *intgMutatingWebhookContext
	)

	BeforeEach(func() {
		ctx = newIntgMutatingWebhookContext()
	})

	AfterEach(func() {
		ctx = nil
	})

	Describe("mutate", func() {
		Context("placeholder", func() {
			AfterEach(func() {
				Expect(ctx.Client.Delete(ctx, ctx.vmGroup)).To(Succeed())
			})

			It("should work", func() {
				err := ctx.Client.Create(ctx, ctx.vmGroup)
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})

	Context("PowerState Mutation", func() {
		AfterEach(func() {
			if ctx.vmGroup.ResourceVersion != "" {
				Expect(ctx.Client.Delete(ctx, ctx.vmGroup)).To(Succeed())
			}
		})

		When("Creating VirtualMachineGroup with non-empty power state", func() {
			BeforeEach(func() {
				ctx.vmGroup.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
			})

			It("Should set last-updated-power-state annotation", func() {
				Expect(ctx.Client.Create(ctx, ctx.vmGroup)).To(Succeed())

				modified := &vmopv1.VirtualMachineGroup{}
				Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(ctx.vmGroup), modified)).To(Succeed())
				Expect(modified.Annotations).To(HaveKey(vmopv1.LastUpdatedPowerStateTimeAnnotation))
				timestamp := modified.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation]
				_, err := time.Parse(time.RFC3339, timestamp)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("Updating VirtualMachineGroup with power state change", func() {
			BeforeEach(func() {
				ctx.vmGroup.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
				Expect(ctx.Client.Create(ctx, ctx.vmGroup)).To(Succeed())
			})

			It("Should update last-updated-power-state annotation", func() {
				modified := &vmopv1.VirtualMachineGroup{}
				Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(ctx.vmGroup), modified)).To(Succeed())

				// Record the original timestamp
				originalTimestamp := modified.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation]
				Expect(originalTimestamp).ToNot(BeEmpty())

				// Change power state
				modified.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
				Expect(ctx.Client.Update(ctx, modified)).To(Succeed())

				// Get the updated object
				updated := &vmopv1.VirtualMachineGroup{}
				Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(ctx.vmGroup), updated)).To(Succeed())

				// Check that the annotation was updated
				Expect(updated.Annotations).To(HaveKey(vmopv1.LastUpdatedPowerStateTimeAnnotation))
				newTimestamp := updated.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation]
				Expect(newTimestamp).NotTo(Equal(originalTimestamp))
				_, err := time.Parse(time.RFC3339Nano, newTimestamp)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("Updating VirtualMachineGroup without power state change", func() {
			BeforeEach(func() {
				ctx.vmGroup.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
				Expect(ctx.Client.Create(ctx, ctx.vmGroup)).To(Succeed())
			})

			It("Should not update last-updated-power-state annotation", func() {
				modified := &vmopv1.VirtualMachineGroup{}
				Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(ctx.vmGroup), modified)).To(Succeed())

				// Record the original timestamp
				originalTimestamp := modified.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation]

				// Update without changing power state
				modified.Labels = map[string]string{"new-label": "value"}
				Expect(ctx.Client.Update(ctx, modified)).To(Succeed())

				// Get the updated object
				updated := &vmopv1.VirtualMachineGroup{}
				Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(ctx.vmGroup), updated)).To(Succeed())

				// Check that the annotation was not updated
				Expect(updated.Annotations).To(HaveKey(vmopv1.LastUpdatedPowerStateTimeAnnotation))
				newTimestamp := updated.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation]
				Expect(newTimestamp).To(Equal(originalTimestamp))
			})
		})
	})
}
