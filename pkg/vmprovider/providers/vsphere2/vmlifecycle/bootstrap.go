// Copyright (c) 2023-2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle

import (
	"fmt"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/task"
	vimTypes "github.com/vmware/govmomi/vim25/types"
	apiEquality "k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/util/cloudinit"
	kubeutil "github.com/vmware-tanzu/vm-operator/pkg/util/kube"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/config"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/constants"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/network"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/resources"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/sysprep"
)

const (
	// OvfEnvironmentTransportGuestInfo is the OVF transport type that uses
	// GuestInfo. The other valid type is "iso".
	OvfEnvironmentTransportGuestInfo = "com.vmware.guestInfo"
)

type BootstrapData struct {
	Data       map[string]string
	VAppData   map[string]string
	VAppExData map[string]map[string]string

	CloudConfig *cloudinit.CloudConfigSecretData
	Sysprep     *sysprep.SecretData
}

type TemplateRenderFunc func(string, string) string

type BootstrapArgs struct {
	BootstrapData

	TemplateRenderFn TemplateRenderFunc
	NetworkResults   network.NetworkInterfaceResults
	Hostname         string
	DNSServers       []string
	SearchSuffixes   []string
}

func DoBootstrap(
	vmCtx context.VirtualMachineContextA2,
	vcVM *object.VirtualMachine,
	config *vimTypes.VirtualMachineConfigInfo,
	k8sClient ctrl.Client,
	networkResults network.NetworkInterfaceResults,
	bootstrapData BootstrapData) error {

	bootstrap := vmCtx.VM.Spec.Bootstrap
	if bootstrap == nil {
		// V1ALPHA1: We always defaulted to LinuxPrep w/ HwClockUTC=true.
		bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
			LinuxPrep: &vmopv1.VirtualMachineBootstrapLinuxPrepSpec{
				HardwareClockIsUTC: true,
			},
		}
	}

	cloudInit := bootstrap.CloudInit
	linuxPrep := bootstrap.LinuxPrep
	sysPrep := bootstrap.Sysprep
	vAppConfig := bootstrap.VAppConfig

	isGOSC := linuxPrep != nil || sysPrep != nil
	bootstrapArgs, err := getBootstrapArgs(vmCtx, k8sClient, cloudInit != nil, isGOSC, networkResults, bootstrapData)
	if err != nil {
		return err
	}

	if sysPrep != nil || vAppConfig != nil {
		bootstrapArgs.TemplateRenderFn = GetTemplateRenderFunc(vmCtx, bootstrapArgs)
	}

	var configSpec *vimTypes.VirtualMachineConfigSpec
	var customSpec *vimTypes.CustomizationSpec

	switch {
	case cloudInit != nil:
		configSpec, customSpec, err = BootStrapCloudInit(vmCtx, config, cloudInit, bootstrapArgs)
	case linuxPrep != nil:
		configSpec, customSpec, err = BootStrapLinuxPrep(vmCtx, config, linuxPrep, vAppConfig, bootstrapArgs)
	case sysPrep != nil:
		configSpec, customSpec, err = BootstrapSysPrep(vmCtx, config, sysPrep, vAppConfig, bootstrapArgs)
	case vAppConfig != nil:
		configSpec, customSpec, err = BootstrapVAppConfig(vmCtx, config, vAppConfig, bootstrapArgs)
	default:
		vmCtx.Logger.V(6).Info("no bootstrap provider specified")
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to create bootstrap data: %w", err)
	}

	if configSpec != nil {
		err := doReconfigure(vmCtx, vcVM, configSpec)
		if err != nil {
			return fmt.Errorf("boostrap reconfigure failed: %w", err)
		}
	}

	if customSpec != nil {
		err := doCustomize(vmCtx, vcVM, config, customSpec)
		if err != nil {
			return fmt.Errorf("boostrap customize failed: %w", err)
		}
	}

	return nil
}

func getBootstrapArgs(
	vmCtx context.VirtualMachineContextA2,
	k8sClient ctrl.Client,
	isCloudInit, isGOSC bool,
	networkResults network.NetworkInterfaceResults,
	bootstrapData BootstrapData) (*BootstrapArgs, error) {

	bootstrapArgs := BootstrapArgs{
		BootstrapData:  bootstrapData,
		NetworkResults: networkResults,
		Hostname:       vmCtx.VM.Name,
	}

	if networkSpec := vmCtx.VM.Spec.Network; networkSpec != nil {
		if networkSpec.HostName != "" {
			bootstrapArgs.Hostname = networkSpec.HostName
		}
		bootstrapArgs.DNSServers = networkSpec.Nameservers
		bootstrapArgs.SearchSuffixes = networkSpec.SearchDomains
	}

	// If the VM is missing DNS info - that is, it did not specify DNS for the interfaces - populate that
	// now from the SV global configuration. Note that the VM is probably OK as long as at least one
	// interface has DNS info, but we would previously set it for every interface so keep doing that
	// here. Similarly, we didn't populate SearchDomains for non-TKG VMs so we don't here either. This is
	// all a little nuts & complicated and probably not correct for every situation.
	isTKG := kubeutil.HasCAPILabels(vmCtx.VM.Labels)
	getDNSInformationFromConfigMap := false
	for _, r := range networkResults.Results {
		if r.DHCP4 || r.DHCP6 {
			continue
		}

		if len(bootstrapArgs.DNSServers) == 0 && len(r.Nameservers) == 0 {
			getDNSInformationFromConfigMap = true
			break
		}

		// V1ALPHA1: Do not default the global search suffixes for LinuxPrep and Sysprep
		// to what is in the ConfigMap.
		if len(r.SearchDomains) == 0 && (isTKG || (!isGOSC && len(bootstrapArgs.SearchSuffixes) == 0)) {
			getDNSInformationFromConfigMap = true
			break
		}
	}

	if getDNSInformationFromConfigMap {
		nameservers, searchSuffixes, err := config.GetDNSInformationFromConfigMap(vmCtx, k8sClient)
		if err != nil && ctrl.IgnoreNotFound(err) != nil {
			// This ConfigMap doesn't exist in certain test envs.
			return nil, err
		}

		if len(bootstrapArgs.DNSServers) == 0 {
			// GOSC will this for its global config.
			bootstrapArgs.DNSServers = nameservers
		}
		if !isGOSC && len(bootstrapArgs.SearchSuffixes) == 0 {
			// See the comment above: we don't apply the global suffixes to GOSC.
			bootstrapArgs.SearchSuffixes = searchSuffixes
		}

		if isCloudInit {
			// Previously we would apply the global DNS config to every interface so do that here too.
			for i := range networkResults.Results {
				r := &networkResults.Results[i]

				if r.DHCP4 || r.DHCP6 {
					continue
				}

				if len(r.Nameservers) == 0 {
					r.Nameservers = nameservers
				}
				// V1ALPHA1: Only apply global search domains to TKG VMs.
				if isTKG && len(r.SearchDomains) == 0 {
					r.SearchDomains = searchSuffixes
				}
			}
		}
	}

	return &bootstrapArgs, nil
}

func doReconfigure(
	vmCtx context.VirtualMachineContextA2,
	vcVM *object.VirtualMachine,
	configSpec *vimTypes.VirtualMachineConfigSpec) error {

	defaultConfigSpec := &vimTypes.VirtualMachineConfigSpec{}
	if !apiEquality.Semantic.DeepEqual(configSpec, defaultConfigSpec) {
		vmCtx.Logger.Info("Customization Reconfigure", "configSpec", configSpec)

		if err := resources.NewVMFromObject(vcVM).Reconfigure(vmCtx, configSpec); err != nil {
			vmCtx.Logger.Error(err, "customization reconfigure failed")
			return err
		}
	}

	return nil
}

func doCustomize(
	vmCtx context.VirtualMachineContextA2,
	vcVM *object.VirtualMachine,
	config *vimTypes.VirtualMachineConfigInfo,
	customSpec *vimTypes.CustomizationSpec) error {

	if vmCtx.VM.Annotations[constants.VSphereCustomizationBypassKey] == constants.VSphereCustomizationBypassDisable {
		vmCtx.Logger.Info("Skipping vsphere customization because of vsphere-customization bypass annotation")
		return nil
	}

	if IsCustomizationPendingExtraConfig(config.ExtraConfig) {
		vmCtx.Logger.Info("Skipping customization because it is already pending")
		// TODO: We should really determine if the pending customization is stale, clear it
		// if so, and then re-customize. Otherwise, the Customize call could perpetually fail
		// preventing power on.
		return nil
	}

	vmCtx.Logger.Info("Customizing VM", "customizationSpec", *customSpec)
	if err := resources.NewVMFromObject(vcVM).Customize(vmCtx, *customSpec); err != nil {
		// isCustomizationPendingExtraConfig() above is supposed to prevent this error, but
		// handle it explicitly here just in case so VM reconciliation can proceed.
		if !isCustomizationPendingError(err) {
			return err
		}
	}

	return nil
}

func IsCustomizationPendingExtraConfig(extraConfig []vimTypes.BaseOptionValue) bool {
	for _, opt := range extraConfig {
		if optValue := opt.GetOptionValue(); optValue != nil {
			if optValue.Key == constants.GOSCPendingExtraConfigKey {
				return optValue.Value.(string) != ""
			}
		}
	}
	return false
}

func isCustomizationPendingError(err error) bool {
	if te, ok := err.(task.Error); ok {
		if _, ok := te.Fault().(*vimTypes.CustomizationPending); ok {
			return true
		}
	}
	return false
}
