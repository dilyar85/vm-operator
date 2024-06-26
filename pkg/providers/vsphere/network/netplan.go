// Copyright (c) 2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"strings"

	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/constants"
)

// Netplan representation described in https://via.vmw.com/cloud-init-netplan // FIXME: 404.
type Netplan struct {
	Version   int                        `json:"version,omitempty"`
	Ethernets map[string]NetplanEthernet `json:"ethernets,omitempty"`
}

type NetplanEthernet struct {
	Match       NetplanEthernetMatch      `json:"match,omitempty"`
	SetName     string                    `json:"set-name,omitempty"`
	Dhcp4       bool                      `json:"dhcp4,omitempty"`
	Dhcp6       bool                      `json:"dhcp6,omitempty"`
	Addresses   []string                  `json:"addresses,omitempty"`
	Gateway4    string                    `json:"gateway4,omitempty"`
	Gateway6    string                    `json:"gateway6,omitempty"`
	MTU         int64                     `json:"mtu,omitempty"`
	Nameservers NetplanEthernetNameserver `json:"nameservers,omitempty"`
	Routes      []NetplanEthernetRoute    `json:"routes,omitempty"`
}

type NetplanEthernetMatch struct {
	MacAddress string `json:"macaddress,omitempty"`
}

type NetplanEthernetNameserver struct {
	Addresses []string `json:"addresses,omitempty"`
	Search    []string `json:"search,omitempty"`
}

type NetplanEthernetRoute struct {
	To     string `json:"to"`
	Via    string `json:"via"`
	Metric int32  `json:"metric,omitempty"`
}

func NetPlanCustomization(result NetworkInterfaceResults) (*Netplan, error) {
	netPlan := &Netplan{
		Version:   constants.NetPlanVersion,
		Ethernets: make(map[string]NetplanEthernet),
	}

	for _, r := range result.Results {
		npEth := NetplanEthernet{
			Match: NetplanEthernetMatch{
				MacAddress: NormalizeNetplanMac(r.MacAddress),
			},
			SetName: r.GuestDeviceName,
			MTU:     r.MTU,
			Nameservers: NetplanEthernetNameserver{
				Addresses: r.Nameservers,
				Search:    r.SearchDomains,
			},
		}

		npEth.Dhcp4 = r.DHCP4
		npEth.Dhcp6 = r.DHCP6

		if !npEth.Dhcp4 {
			for _, ipConfig := range r.IPConfigs {
				if ipConfig.IsIPv4 {
					if npEth.Gateway4 == "" {
						npEth.Gateway4 = ipConfig.Gateway
					}
					npEth.Addresses = append(npEth.Addresses, ipConfig.IPCIDR)
				}
			}
		}
		if !npEth.Dhcp6 {
			for _, ipConfig := range r.IPConfigs {
				if !ipConfig.IsIPv4 {
					if npEth.Gateway6 == "" {
						npEth.Gateway6 = ipConfig.Gateway
					}
					npEth.Addresses = append(npEth.Addresses, ipConfig.IPCIDR)
				}
			}
		}

		for _, route := range r.Routes {
			npEth.Routes = append(npEth.Routes, NetplanEthernetRoute(route))
		}

		netPlan.Ethernets[r.Name] = npEth
	}

	return netPlan, nil
}

// NormalizeNetplanMac normalizes the mac address format to one compatible with netplan.
func NormalizeNetplanMac(mac string) string {
	mac = strings.ReplaceAll(mac, "-", ":")
	return strings.ToLower(mac)
}
