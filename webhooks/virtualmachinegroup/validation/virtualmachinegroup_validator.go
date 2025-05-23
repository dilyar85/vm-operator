// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"fmt"
	"net/http"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmgr "sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha4"
	"github.com/vmware-tanzu/vm-operator/pkg/builder"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/webhooks/common"
)

const (
	webHookName                           = "default"
	modifyAnnotationNotAllowedForNonAdmin = "modifying this annotation is not allowed for non-admin users"
	emptyPowerStateNotAllowedAfterSet     = "cannot set powerState to empty once it's been set"
	invalidTimeFormat                     = "time must be in RFC3339Nano format"
	memberNotFoundInGroup                 = "member not found in group"
)

// +kubebuilder:webhook:verbs=create;update,path=/default-validate-vmoperator-vmware-com-v1alpha4-virtualmachinegroup,mutating=false,failurePolicy=fail,groups=vmoperator.vmware.com,resources=virtualmachinegroups,versions=v1alpha4,name=default.validating.virtualmachinegroup.v1alpha4.vmoperator.vmware.com,sideEffects=None,admissionReviewVersions=v1;v1beta1
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachinegroups,verbs=get;list
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachinegroups/status,verbs=get

// AddToManager adds the webhook to the provided manager.
func AddToManager(ctx *pkgctx.ControllerManagerContext, mgr ctrlmgr.Manager) error {
	hook, err := builder.NewValidatingWebhook(ctx, mgr, webHookName, NewValidator(mgr.GetClient()))
	if err != nil {
		return fmt.Errorf("failed to create VirtualMachineGroup validation webhook: %w", err)
	}
	mgr.GetWebhookServer().Register(hook.Path, hook)

	return nil
}

// NewValidator returns the package's Validator.
func NewValidator(client client.Client) builder.Validator {
	return validator{
		client:    client,
		converter: runtime.DefaultUnstructuredConverter,
	}
}

type validator struct {
	client    client.Client
	converter runtime.UnstructuredConverter
}

// vmGroupFromUnstructured returns the VirtualMachineGroup from the unstructured object.
func (v validator) vmGroupFromUnstructured(obj runtime.Unstructured) (*vmopv1.VirtualMachineGroup, error) {
	vmGroup := &vmopv1.VirtualMachineGroup{}
	if err := v.converter.FromUnstructured(obj.UnstructuredContent(), vmGroup); err != nil {
		return nil, err
	}
	return vmGroup, nil
}

func (v validator) For() schema.GroupVersionKind {
	return vmopv1.GroupVersion.WithKind(reflect.TypeOf(vmopv1.VirtualMachineGroup{}).Name())
}

func (v validator) ValidateCreate(ctx *pkgctx.WebhookRequestContext) admission.Response {
	vmGroup, err := v.vmGroupFromUnstructured(ctx.Obj)
	if err != nil {
		return webhook.Errored(http.StatusBadRequest, err)
	}

	var fieldErrs field.ErrorList

	fieldErrs = append(fieldErrs, v.validateAnnotation(ctx, vmGroup, nil)...)
	fieldErrs = append(fieldErrs, v.validatePowerOp(ctx, vmGroup, nil)...)

	validationErrs := make([]string, 0, len(fieldErrs))
	for _, fieldErr := range fieldErrs {
		validationErrs = append(validationErrs, fieldErr.Error())
	}

	return common.BuildValidationResponse(ctx, nil, validationErrs, nil)
}

func (v validator) ValidateDelete(*pkgctx.WebhookRequestContext) admission.Response {
	return admission.Allowed("")
}

func (v validator) ValidateUpdate(ctx *pkgctx.WebhookRequestContext) admission.Response {
	vmGroup, err := v.vmGroupFromUnstructured(ctx.Obj)
	if err != nil {
		return webhook.Errored(http.StatusBadRequest, err)
	}

	oldVMGroup, err := v.vmGroupFromUnstructured(ctx.OldObj)
	if err != nil {
		return webhook.Errored(http.StatusBadRequest, err)
	}

	var fieldErrs field.ErrorList

	fieldErrs = append(fieldErrs, v.validateAnnotation(ctx, vmGroup, oldVMGroup)...)
	fieldErrs = append(fieldErrs, v.validatePowerState(ctx, vmGroup, oldVMGroup)...)
	fieldErrs = append(fieldErrs, v.validatePowerOp(ctx, vmGroup, oldVMGroup)...)
	validationErrs := make([]string, 0, len(fieldErrs))
	for _, fieldErr := range fieldErrs {
		validationErrs = append(validationErrs, fieldErr.Error())
	}

	return common.BuildValidationResponse(ctx, nil, validationErrs, nil)
}

func (v validator) validateAnnotation(ctx *pkgctx.WebhookRequestContext, vmGroup, oldVMGroup *vmopv1.VirtualMachineGroup) field.ErrorList {
	var allErrs field.ErrorList

	// Use an empty VMGroup if the oldVMGroup is nil to validate a creation request.
	if oldVMGroup == nil {
		oldVMGroup = &vmopv1.VirtualMachineGroup{}
	}

	annotationPath := field.NewPath("metadata", "annotations")

	// Disallow if last-updated-power-state annotation was modified by a non-admin user.
	if vmGroup.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation] != oldVMGroup.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation] &&
		!ctx.IsPrivilegedAccount {
		allErrs = append(allErrs, field.Forbidden(
			annotationPath.Key(vmopv1.LastUpdatedPowerStateTimeAnnotation),
			modifyAnnotationNotAllowedForNonAdmin))
	}

	// Disallow if the last-updated-power-state annotation value is not in RFC3339Nano format.
	if val := vmGroup.Annotations[vmopv1.LastUpdatedPowerStateTimeAnnotation]; val != "" {
		if _, err := time.Parse(time.RFC3339Nano, val); err != nil {
			allErrs = append(allErrs, field.Invalid(
				annotationPath.Key(vmopv1.LastUpdatedPowerStateTimeAnnotation),
				val,
				invalidTimeFormat))
		}
	}

	return allErrs
}

func (v validator) validatePowerState(_ *pkgctx.WebhookRequestContext, vmGroup, oldVMGroup *vmopv1.VirtualMachineGroup) field.ErrorList {
	var allErrs field.ErrorList

	powerStatePath := field.NewPath("spec", "powerState")

	// Disallow if the powerState is set to empty after it's been set.
	if oldVMGroup.Spec.PowerState != "" && vmGroup.Spec.PowerState == "" {
		allErrs = append(allErrs, field.Forbidden(powerStatePath, emptyPowerStateNotAllowedAfterSet))
	}

	return allErrs
}

// validatePowerOp validates the members in the PowerOnOp and PowerOffOp fields
// are a subset of the group's members.
func (v validator) validatePowerOp(_ *pkgctx.WebhookRequestContext, vmGroup, oldVMGroup *vmopv1.VirtualMachineGroup) field.ErrorList {
	var allErrs field.ErrorList

	if oldVMGroup == nil {
		oldVMGroup = &vmopv1.VirtualMachineGroup{
			Spec: vmopv1.VirtualMachineGroupSpec{
				PowerOnOp:  []vmopv1.VirtualMachineGroupPowerOp{},
				PowerOffOp: []vmopv1.VirtualMachineGroupPowerOp{},
			},
		}
	}

	// Skip if the VMGroup is either created with empty PowerOnOp and PowerOffOp
	// or updated with the same PowerOnOp and PowerOffOp.
	if reflect.DeepEqual(vmGroup.Spec.PowerOnOp, oldVMGroup.Spec.PowerOnOp) &&
		reflect.DeepEqual(vmGroup.Spec.PowerOffOp, oldVMGroup.Spec.PowerOffOp) {
		return allErrs
	}

	vmMembers := make(map[string]struct{}, len(vmGroup.Spec.Members))
	groupMembers := make(map[string]struct{}, len(vmGroup.Spec.Members))
	for _, member := range vmGroup.Spec.Members {
		if member.Kind == "VirtualMachine" {
			vmMembers[member.Name] = struct{}{}
		} else if member.Kind == "VirtualMachineGroup" {
			groupMembers[member.Name] = struct{}{}
		}
	}

	powerOnOpPath := field.NewPath("spec", "powerOnOp")
	for _, powerOp := range vmGroup.Spec.PowerOnOp {
		if _, ok := vmMembers[powerOp.Name]; !ok {
			allErrs = append(allErrs, field.Invalid(powerOnOpPath, powerOp.Name, memberNotFoundInGroup))
		}
	}

	powerOffOpPath := field.NewPath("spec", "powerOffOp")
	for _, powerOp := range vmGroup.Spec.PowerOffOp {
		if _, ok := vmMembers[powerOp.Name]; !ok {
			allErrs = append(allErrs, field.Invalid(powerOffOpPath, powerOp.Name, memberNotFoundInGroup))
		}
	}

	return allErrs
}
