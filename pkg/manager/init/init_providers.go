// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"fmt"

	ctrlmgr "sigs.k8s.io/controller-runtime/pkg/manager"

	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	vsphere2 "github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere"
	"github.com/vmware-tanzu/vm-operator/pkg/record"
)

func InitializeProviders(
	ctx *pkgctx.ControllerManagerContext,
	mgr ctrlmgr.Manager) error {

	vmProviderName := fmt.Sprintf("%s/%s/vmProvider", ctx.Namespace, ctx.Name)
	recorder := record.New(mgr.GetEventRecorderFor(vmProviderName))
	ctx.VMProvider = vsphere2.NewVSphereVMProviderFromClient(ctx, mgr.GetClient(), mgr.GetAPIReader(), recorder)
	return nil
}
