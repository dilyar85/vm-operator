// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kubeutil "github.com/vmware-tanzu/vm-operator/pkg/util/kube"
)

var _ = Describe("VM", func() {

	Context("HasCAPILabels", func() {

		It("should return true if the VM has a CAPW label", func() {
			vmLabels := map[string]string{
				kubeutil.CAPWClusterRoleLabelKey: "",
			}
			Expect(kubeutil.HasCAPILabels(vmLabels)).To(BeTrue())
		})

		It("should return true if the VM has a CAPV label", func() {
			vmLabels := map[string]string{
				kubeutil.CAPVClusterRoleLabelKey: "",
			}
			Expect(kubeutil.HasCAPILabels(vmLabels)).To(BeTrue())
		})

		It("should return false if the VM has no Cluster API related labels", func() {
			vmLabels := map[string]string{}
			Expect(kubeutil.HasCAPILabels(vmLabels)).To(BeFalse())
		})
	})
})
