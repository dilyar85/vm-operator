// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package mutation_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha4"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/test/builder"
	"github.com/vmware-tanzu/vm-operator/webhooks/virtualmachinegroup/mutation"
)

func uniTests() {
	Describe(
		"Mutate",
		Label(
			testlabels.Create,
			testlabels.Update,
			testlabels.Delete,
			testlabels.API,
			testlabels.Mutation,
			testlabels.Webhook,
		),
		unitTestsMutating,
	)
}

type unitMutationWebhookContext struct {
	builder.UnitTestContextForMutatingWebhook
	vmGroup *vmopv1.VirtualMachineGroup
}

func newUnitTestContextForMutatingWebhook() *unitMutationWebhookContext {
	vmGroup := &vmopv1.VirtualMachineGroup{}
	obj, err := builder.ToUnstructured(vmGroup)
	Expect(err).ToNot(HaveOccurred())

	return &unitMutationWebhookContext{
		UnitTestContextForMutatingWebhook: *suite.NewUnitTestContextForMutatingWebhook(obj),
		vmGroup:                           vmGroup,
	}
}

func unitTestsMutating() {
	var (
		ctx *unitMutationWebhookContext
	)

	BeforeEach(func() {
		ctx = newUnitTestContextForMutatingWebhook()
	})

	Describe("VirtualMachineGroupMutator should admit updates when object is under deletion", func() {
		Context("when update request comes in while deletion in progress ", func() {
			It("should admit update operation", func() {
				t := metav1.Now()
				ctx.WebhookRequestContext.Obj.SetDeletionTimestamp(&t)
				response := ctx.Mutate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeTrue())
			})
		})
	})

	Describe("ProcessPowerState", func() {
		Context("Creation", func() {
			When("PowerState is set", func() {
				It("Should add the last-updated-power-state annotation", func() {
					ctx.vmGroup.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn

					result := mutation.ProcessPowerState(&ctx.WebhookRequestContext, ctx.vmGroup, nil)
					Expect(result).To(BeTrue())
					Expect(ctx.vmGroup.Annotations).To(HaveKey(vmopv1.LastUpdatedPowerStateTimeAnnotation))
					timestamp := ctx.vmGroup.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation]
					_, err := time.Parse(time.RFC3339, timestamp)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			When("PowerState is not set", func() {
				It("Should not add the last-updated-power-state annotation", func() {
					result := mutation.ProcessPowerState(&ctx.WebhookRequestContext, ctx.vmGroup, nil)
					Expect(result).To(BeFalse())
				})
			})
		})

		Context("Update", func() {
			When("PowerState changes", func() {
				It("Should add the last-updated-power-state annotation", func() {
					oldVMGroup := ctx.vmGroup.DeepCopy()
					oldVMGroup.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
					ctx.vmGroup.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn

					result := mutation.ProcessPowerState(&ctx.WebhookRequestContext, ctx.vmGroup, oldVMGroup)
					Expect(result).To(BeTrue())
					Expect(ctx.vmGroup.Annotations).To(HaveKey(vmopv1.LastUpdatedPowerStateTimeAnnotation))
					timestamp := ctx.vmGroup.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation]
					_, err := time.Parse(time.RFC3339Nano, timestamp)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			When("PowerState does not change", func() {
				It("Should not modify annotations", func() {
					result := mutation.ProcessPowerState(&ctx.WebhookRequestContext, ctx.vmGroup, nil)
					Expect(result).To(BeFalse())
				})
			})
		})
	})
}
