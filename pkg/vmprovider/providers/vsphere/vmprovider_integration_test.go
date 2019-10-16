// +build integration

// Copyright (c) 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vsphere_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	vmoperatorv1alpha1 "github.com/vmware-tanzu/vm-operator/pkg/apis/vmoperator/v1alpha1"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere"
	"github.com/vmware-tanzu/vm-operator/test/integration"
)

var _ = Describe("VMProvider Tests", func() {
	Context("Creating a VM via vmprovider", func() {
		It("should correctly update VirtualMachineStatus", func() {
			testNamespace := "test-namespace-vmp"
			testVMName := "test-vm-vmp"

			// Create a new VMProvder from the config provided by the test
			vmProvider, err := vsphere.NewVSphereVmProviderFromConfig(testNamespace, config)
			Expect(err).NotTo(HaveOccurred())

			// Instruction to vcsim to give the VM an IP address, otherwise CreateVirtualMachine fails
			testIP := "10.0.0.1"
			vmMetadata := map[string]string{"SET.guest.ipAddress": testIP}
			imageName := "" // create, not clone
			vmClass := getVMClassInstance(testVMName, testNamespace)
			vm := getVirtualMachineInstance(testVMName, testNamespace, imageName, vmClass.Name)
			Expect(vm.Status.BiosUuid).Should(BeEmpty())

			// Note that createVirtualMachine has the side effect of changing the vm input value
			err = vmProvider.CreateVirtualMachine(context.TODO(), vm, *vmClass, vmMetadata, "testProfileID")
			Expect(err).NotTo(HaveOccurred())
			Expect(vm.Status.VmIp).Should(Equal(testIP))
			Expect(vm.Status.PowerState).Should(Equal(vmoperatorv1alpha1.VirtualMachinePoweredOn))
			Expect(vm.Status.BiosUuid).ShouldNot(BeEmpty())
		})
	})
	Context("Creating and Updating a VM from Content Library", func() {
		It("reconfigure and powerON without errors", func() {
			testNamespace := "test-namespace-vmp"
			testVMName := "test-vm-vmp-deploy"

			//Setting VM Operator config to use CL
			config.ContentSource = integration.GetContentSourceID()

			// Create a new VMProvder from the config provided by the test
			vmProvider, err := vsphere.NewVSphereVmProviderFromConfig(testNamespace, config)
			Expect(err).NotTo(HaveOccurred())

			// Instruction to vcsim to give the VM an IP address, otherwise CreateVirtualMachine fails
			testIP := "10.0.0.1"
			vmMetadata := map[string]string{"SET.guest.ipAddress": testIP}
			imageName := "test-item" // create, not clone
			vmClass := getVMClassInstance(testVMName, testNamespace)
			vm := getVirtualMachineInstance(testVMName, testNamespace, imageName, vmClass.Name)
			Expect(vm.Status.BiosUuid).Should(BeEmpty())

			// CreateVirtualMachine from CL
			err = vmProvider.CreateVirtualMachine(context.TODO(), vm, *vmClass, vmMetadata, "testProfileID")
			Expect(err).NotTo(HaveOccurred())
			Expect(vm.Status.PowerState).Should(Equal(vmoperatorv1alpha1.VirtualMachinePoweredOff))
			// Update Virtual Machine to Reconfigure with VM Class config
			err = vmProvider.UpdateVirtualMachine(context.TODO(), vm, *vmClass, vmMetadata)
			Expect(vm.Status.VmIp).Should(Equal(testIP))
			Expect(vm.Status.PowerState).Should(Equal(vmoperatorv1alpha1.VirtualMachinePoweredOn))
			Expect(vm.Status.BiosUuid).ShouldNot(BeEmpty())
		})
	})
})