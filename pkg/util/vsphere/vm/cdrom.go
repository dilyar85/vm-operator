// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"fmt"
	"strings"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	imgregv1a1 "github.com/vmware-tanzu/image-registry-operator-api/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha3"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/util"
)

const (
	vmiKind  = "VirtualMachineImage"
	cvmiKind = "Cluster" + vmiKind

	serverIDSuffix = "?serverId="
)

// UpdateCdromDeviceChanges reconciles the desired CD-ROM devices specified in
// VM.Spec.Cdrom with the current CD-ROM devices in the VM. It returns a list of
// device changes required to update the CD-ROM devices.
func UpdateCdromDeviceChanges(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	curDevices object.VirtualDeviceList) ([]vimtypes.BaseVirtualDeviceConfigSpec, error) {
	var (
		deviceChanges                  []vimtypes.BaseVirtualDeviceConfigSpec
		curCdromBackingFileNameToSpec  = make(map[string]vmopv1.VirtualMachineCdromSpec)
		expectedBackingFileNameToCdrom = make(map[string]vimtypes.BaseVirtualDevice, len(vmCtx.VM.Spec.Cdrom))
	)

	for _, specCdrom := range vmCtx.VM.Spec.Cdrom {
		imageRef := specCdrom.Image
		if imageRef == nil {
			return nil, fmt.Errorf("image ref is nil for CD-ROM %s", specCdrom.Name)
		}

		cdrom, bFileName, err := getCdromAndBackingByImgRef(vmCtx, client, *imageRef, curDevices)
		if err != nil {
			return nil, err
		}

		if cdrom != nil {
			// CD-ROM already exists, store it to update connection state later.
			curCdromBackingFileNameToSpec[bFileName] = specCdrom
		} else {
			// CD-ROM does not exist, create a new one with desired backing and
			// connection, and update the device changes with "Add" operation.
			var err error
			cdrom, err = createNewCdrom(specCdrom, bFileName)
			if err != nil {
				return nil, err
			}

			deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
				Device:    cdrom,
				Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
			})
		}

		expectedBackingFileNameToCdrom[bFileName] = cdrom
	}

	// Ensure all CD-ROM devices are assigned to controllers with proper keys.
	// This may require new controllers to be added if no available slots.
	newControllers := ensureAllCdromsHaveControllers(expectedBackingFileNameToCdrom, curDevices)
	deviceChanges = append(deviceChanges, newControllers...)

	// Update existing CD-ROM devices' connection state if changed.
	// Add them to the device changes with "Edit" operation.
	curCdromChanges := updateCurCdromsConnectionState(
		curCdromBackingFileNameToSpec,
		expectedBackingFileNameToCdrom,
	)
	deviceChanges = append(deviceChanges, curCdromChanges...)

	// Delete any existing CD-ROM devices that are not in the expected list.
	// Add them to the device changes with "Remove" operation.
	curCdroms := util.SelectDevicesByType[*vimtypes.VirtualCdrom](curDevices)
	for _, cdrom := range curCdroms {
		if b, ok := cdrom.Backing.(*vimtypes.VirtualCdromIsoBackingInfo); ok {
			if _, ok := expectedBackingFileNameToCdrom[b.FileName]; ok {
				continue
			}
		}

		deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
			Device:    cdrom,
			Operation: vimtypes.VirtualDeviceConfigSpecOperationRemove,
		})
	}

	return deviceChanges, nil
}

// UpdateConfigSpecCdromDeviceConnection updates the connection state of the
// VM's existing CD-ROM devices to match the desired connection state specified
// in the VM.Spec.Cdrom list.
func UpdateConfigSpecCdromDeviceConnection(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	config *vimtypes.VirtualMachineConfigInfo,
	configSpec *vimtypes.VirtualMachineConfigSpec) error {
	var (
		cdromSpec                    = vmCtx.VM.Spec.Cdrom
		curDevices                   = object.VirtualDeviceList(config.Hardware.Device)
		backingFileNameToCdromSpec   = make(map[string]vmopv1.VirtualMachineCdromSpec, len(cdromSpec))
		backingFileNameToCdromDevice = make(map[string]vimtypes.BaseVirtualDevice, len(cdromSpec))
	)

	for _, specCdrom := range cdromSpec {
		imageRef := specCdrom.Image
		if imageRef == nil {
			return fmt.Errorf("image ref is nil for CD-ROM %s", specCdrom.Name)
		}

		cdrom, bFileName, err := getCdromAndBackingByImgRef(vmCtx, client, *imageRef, curDevices)
		if err != nil {
			return err
		}

		if cdrom == nil {
			return fmt.Errorf("no CD-ROM is found for image ref %s", *imageRef)
		}

		backingFileNameToCdromSpec[bFileName] = specCdrom
		backingFileNameToCdromDevice[bFileName] = cdrom
	}

	curCdromChanges := updateCurCdromsConnectionState(
		backingFileNameToCdromSpec,
		backingFileNameToCdromDevice,
	)
	configSpec.DeviceChange = append(configSpec.DeviceChange, curCdromChanges...)
	return nil
}

// getCdromAndBackingByImgRef returns the CD-ROM device and its backing file
// name from the given image reference.
func getCdromAndBackingByImgRef(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	imageRef vmopv1.VirtualMachineImageRef,
	curDevices object.VirtualDeviceList) (vimtypes.BaseVirtualDevice, string, error) {

	fileName, err := getIsoFilenameFromImageRef(vmCtx, client, imageRef)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get the ISO file name to find CD-ROM: %w", err)
	}

	backing := &vimtypes.VirtualCdromIsoBackingInfo{
		VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
			FileName: fileName,
		},
	}
	cdrom := curDevices.SelectByBackingInfo(backing)
	switch len(cdrom) {
	case 0:
		return nil, fileName, nil
	case 1:
		return cdrom[0], fileName, nil
	default:
		return nil, fileName, fmt.Errorf("found multiple CD-ROMs with same backing from image ref: %v", imageRef)
	}
}

// createNewCdrom creates a new CD-ROM device with the given backing file name
// and connection state specified in the VirtualMachineCdromSpec.
func createNewCdrom(
	cdromSpec vmopv1.VirtualMachineCdromSpec,
	backingFileName string) (*vimtypes.VirtualCdrom, error) {
	backing := &vimtypes.VirtualCdromIsoBackingInfo{
		VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
			FileName: backingFileName,
		},
	}

	cdrom := &vimtypes.VirtualCdrom{
		VirtualDevice: vimtypes.VirtualDevice{
			Backing: backing,
			Connectable: &vimtypes.VirtualDeviceConnectInfo{
				AllowGuestControl: cdromSpec.AllowGuestControl,
				StartConnected:    !cdromSpec.Disconnect,
			},
		},
	}

	return cdrom, nil
}

// ensureAllCdromsHaveControllers ensures all CD-ROM device are assigned to an
// available controller on the VM. It first connects to one of the VM's two IDE
// channels if one is free. If they are both in use, connect to the VM's SATA
// controller if one is present. Otherwise, add a new AHCI (SATA) controller to
// the VM and assign the CD-ROM to it.
func ensureAllCdromsHaveControllers(
	expectedCdromDevices map[string]vimtypes.BaseVirtualDevice,
	curDevices object.VirtualDeviceList) []vimtypes.BaseVirtualDeviceConfigSpec {
	var newControllerChanges []vimtypes.BaseVirtualDeviceConfigSpec

	for _, d := range expectedCdromDevices {
		if ide := curDevices.PickController((*vimtypes.VirtualIDEController)(nil)); ide != nil {
			curDevices.AssignController(d, ide)
		} else if sata := curDevices.PickController((*vimtypes.VirtualSATAController)(nil)); sata != nil {
			curDevices.AssignController(d, sata)
		} else {
			ahci, controllerChanges, updatedCurDevices := addNewAHCIController(curDevices)
			// Update curDevices so that next CD-ROMs can be assigned to this
			// new AHCI controller.
			curDevices = updatedCurDevices
			curDevices.AssignController(d, ahci)
			newControllerChanges = append(newControllerChanges, controllerChanges...)
		}
	}

	return newControllerChanges
}

// addNewAHCIController adds a new AHCI (SATA) controller to the VM and returns
// the AHCI controller and the other device changes required to add it to VM.
func addNewAHCIController(curDevices object.VirtualDeviceList) (
	*vimtypes.VirtualAHCIController,
	[]vimtypes.BaseVirtualDeviceConfigSpec,
	object.VirtualDeviceList) {
	var (
		pciController *vimtypes.VirtualPCIController
		deviceChanges []vimtypes.BaseVirtualDeviceConfigSpec
	)

	// Get PCI controller key which is required to add a new SATA controller.
	if existingPCI := curDevices.PickController(
		(*vimtypes.VirtualPCIController)(nil)); existingPCI != nil {
		pciController = existingPCI.(*vimtypes.VirtualPCIController)
	} else {
		// PCI controller is not present, create a new one.
		pciController = &vimtypes.VirtualPCIController{
			VirtualController: vimtypes.VirtualController{
				VirtualDevice: vimtypes.VirtualDevice{
					Key: curDevices.NewKey(),
				},
			},
		}
		deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
			Device:    pciController,
			Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
		})
		// Add the new PCI controller to ensure curDevices.NewKey() is updated.
		curDevices = append(curDevices, pciController)
	}

	ahciController := &vimtypes.VirtualAHCIController{
		VirtualSATAController: vimtypes.VirtualSATAController{
			VirtualController: vimtypes.VirtualController{
				VirtualDevice: vimtypes.VirtualDevice{
					ControllerKey: pciController.ControllerKey,
					Key:           curDevices.NewKey(),
				},
			},
		},
	}
	deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
		Device:    ahciController,
		Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
	})
	curDevices = append(curDevices, ahciController)

	return ahciController, deviceChanges, curDevices
}

// getIsoFilenameFromImageRef returns the ISO type content library file name
// based on the given VirtualMachineImageRef.
// If a namespace scope VirtualMachineImage is provided, it checks the
// ContentLibraryItem status, otherwise, a ClusterVirtualMachineImage status, to
// get the ISO file storage URI without the server ID suffix.
func getIsoFilenameFromImageRef(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	imageRef vmopv1.VirtualMachineImageRef) (string, error) {
	var itemStatus imgregv1a1.ContentLibraryItemStatus
	switch imageRef.Kind {
	case vmiKind:
		ns := vmCtx.VM.Namespace
		var vmi vmopv1.VirtualMachineImage
		if err := client.Get(vmCtx, ctrlclient.ObjectKey{Name: imageRef.Name, Namespace: ns}, &vmi); err != nil {
			return "", err
		}
		var clitem imgregv1a1.ContentLibraryItem
		if err := client.Get(vmCtx, ctrlclient.ObjectKey{Name: vmi.Spec.ProviderRef.Name, Namespace: ns}, &clitem); err != nil {
			return "", err
		}
		itemStatus = clitem.Status
	case cvmiKind:
		var cvmi vmopv1.ClusterVirtualMachineImage
		if err := client.Get(vmCtx, ctrlclient.ObjectKey{Name: imageRef.Name}, &cvmi); err != nil {
			return "", err
		}
		var cclitem imgregv1a1.ClusterContentLibraryItem
		if err := client.Get(vmCtx, ctrlclient.ObjectKey{Name: cvmi.Spec.ProviderRef.Name}, &cclitem); err != nil {
			return "", err
		}
		itemStatus = cclitem.Status
	}

	if itemStatus.Type != imgregv1a1.ContentLibraryItemTypeIso {
		return "", fmt.Errorf("expected ISO type image, got %s", itemStatus.Type)
	}
	if len(itemStatus.FileInfo) == 0 || itemStatus.FileInfo[0].StorageURI == "" {
		return "", fmt.Errorf("no storage URI found in the content library item of image")
	}

	// Currently the storage URI is in the following format:
	// "ds:///vmfs/volumes/...{file_id}.iso?serverId=..."
	// vCenter converts this to a different format during the CD-ROM creation:
	// "[sharedVmfs-0] contentlib-${lib_id}/${item_id}/...{file_id}.iso"
	//
	// TODO (Sai): Return the converted format after govmomi supports it.
	uri := itemStatus.FileInfo[0].StorageURI
	if idex := strings.Index(uri, serverIDSuffix); idex != -1 {
		uri = uri[:idex]
	}

	return uri, nil
}

// updateCurCdromsConnectionState updates the connection state of the given
// CD-ROM devices to match the desired connection state in the given spec.
func updateCurCdromsConnectionState(
	backingFileNameToCdromSpec map[string]vmopv1.VirtualMachineCdromSpec,
	backingFileNameToCdrom map[string]vimtypes.BaseVirtualDevice) []vimtypes.BaseVirtualDeviceConfigSpec {
	if len(backingFileNameToCdromSpec) == 0 || len(backingFileNameToCdrom) == 0 {
		return nil
	}

	var deviceChanges []vimtypes.BaseVirtualDeviceConfigSpec

	for b, spec := range backingFileNameToCdromSpec {
		if cdrom, ok := backingFileNameToCdrom[b]; ok {
			if c := cdrom.GetVirtualDevice().Connectable; c != nil &&
				(c.Connected == spec.Disconnect || c.AllowGuestControl != spec.AllowGuestControl) {
				c.StartConnected = !spec.Disconnect
				c.AllowGuestControl = spec.AllowGuestControl
				deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
					Device:    cdrom,
					Operation: vimtypes.VirtualDeviceConfigSpecOperationEdit,
				})
			}
		}
	}

	return deviceChanges
}
