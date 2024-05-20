// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package static_route_isis_redistribution_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/open-traffic-generator/snappi/gosnappi"
	"github.com/openconfig/featureprofiles/internal/deviations"
	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/featureprofiles/internal/isissession"
	"github.com/openconfig/featureprofiles/internal/otgutils"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"
	"github.com/openconfig/ygnmi/ygnmi"
	"github.com/openconfig/ygot/ygot"
)

const (
	lossTolerance   = float64(1)
	ipv4PrefixLen   = 30
	ipv6PrefixLen   = 126
	v4Route         = "192.168.10.0"
	v4TrafficStart  = "192.168.10.1"
	v4RoutePrefix   = uint32(24)
	v6Route         = "2024:db8:128:128::"
	v6TrafficStart  = "2024:db8:128:128::1"
	v6RoutePrefix   = uint32(64)
	dp2v4Route      = "192.168.1.4"
	dp2v4Prefix     = uint32(30)
	dp2v6Route      = "2001:DB8::0"
	dp2v6Prefix     = uint32(126)
	v4Flow          = "v4Flow"
	v6Flow          = "v6Flow"
	trafficDuration = 30 * time.Second
	prefixMatch     = "exact"
	v4RoutePolicy   = "route-policy-v4"
	v4Statement     = "statement-v4"
	v4PrefixSet     = "prefix-set-v4"
	v6RoutePolicy   = "route-policy-v6"
	v6Statement     = "statement-v6"
	v6PrefixSet     = "prefix-set-v6"
	protoSrc        = oc.PolicyTypes_INSTALL_PROTOCOL_TYPE_STATIC
	protoDst        = oc.PolicyTypes_INSTALL_PROTOCOL_TYPE_ISIS
	dummyV6         = "2001:db8::192:0:2:d"
	dummyMAC        = "00:1A:11:00:0A:BC"
)

var (
	advertisedIPv4 = ipAddr{address: dp2v4Route, prefix: dp2v4Prefix}
	advertisedIPv6 = ipAddr{address: dp2v6Route, prefix: dp2v6Prefix}
)

func TestMain(m *testing.M) {
	fptest.RunTests(m)
}

type ipAddr struct {
	address string
	prefix  uint32
}

type TableConnectionConfig struct {
	ImportPolicy             []string `json:"import-policy"`
	DisableMetricPropagation bool     `json:"disable-metric-propagation"`
	DstProtocol              string   `json:"dst-protocol"`
	AddressFamily            string   `json:"address-family"`
	SrcProtocol              string   `json:"src-protocol"`
}

func getAndVerifyIsisImportPolicy(t *testing.T,
	dut *ondatra.DUTDevice, DisableMetricValue bool,
	RplName string, addressFamily string) {

	gnmiClient := dut.RawAPIs().GNMI(t)
	getResponse, err := gnmiClient.Get(context.Background(), &gpb.GetRequest{
		Path: []*gpb.Path{{
			Elem: []*gpb.PathElem{
				{Name: "network-instances"},
				{Name: "network-instance", Key: map[string]string{"name": "DEFAULT"}},
				{Name: "table-connections"},
				{Name: "table-connection", Key: map[string]string{
					"src-protocol":   "STATIC",
					"dst-protocol":   "ISIS",
					"address-family": addressFamily}},
				{Name: "config"},
			},
		}},
		Type:     gpb.GetRequest_CONFIG,
		Encoding: gpb.Encoding_JSON_IETF,
	})

	if err != nil {
		t.Fatalf("failed due to %v", err)
	}
	t.Log(getResponse)

	t.Log("Verify Get outputs ")
	for _, notification := range getResponse.Notification {
		for _, update := range notification.Update {
			if update.Path != nil {
				var config TableConnectionConfig
				err = json.Unmarshal(update.Val.GetJsonIetfVal(), &config)
				if err != nil {
					t.Fatalf("Failed to unmarshal JSON: %v", err)
				}
				if config.SrcProtocol != "openconfig-policy-types:STATIC" {
					t.Fatalf("src-protocol is not set to STATIC as expected")
				}
				if config.DstProtocol != "openconfig-policy-types:ISIS" {
					t.Fatalf("dst-protocol is not set to ISIS as expected")
				}
				addressFamilyMatchString := fmt.Sprintf("openconfig-types:%s", addressFamily)
				if config.AddressFamily != addressFamilyMatchString {
					t.Fatalf("address-family is not set to %s as expected", addressFamily)
				}
				if config.DisableMetricPropagation != DisableMetricValue {
					t.Fatalf("disable-metric-propagation is not set to %v as expected", DisableMetricValue)
				}
				for _, i := range config.ImportPolicy {
					if i != RplName {
						t.Fatalf("import-policy is not set to %s as expected", RplName)
					}
				}
				t.Logf("Table Connection Details:"+
					"SRC PROTO GOT %v WANT STATIC\n"+
					"DST PRTO GOT %v WANT ISIS\n"+
					"ADDRESS FAMILY GOT %v WANT %v\n"+
					"DISABLEMETRICPROPAGATION GOT %v WANT %v\n", config.SrcProtocol,
					config.DstProtocol, config.AddressFamily, addressFamily,
					config.DisableMetricPropagation, DisableMetricValue)
			}
		}
	}
}

func isisImportPolicyConfig(t *testing.T, dut *ondatra.DUTDevice, policyName string,
	srcProto oc.E_PolicyTypes_INSTALL_PROTOCOL_TYPE,
	dstProto oc.E_PolicyTypes_INSTALL_PROTOCOL_TYPE,
	addfmly oc.E_Types_ADDRESS_FAMILY,
	metricPropagation bool) {

	t.Log("configure redistribution under isis")

	dni := deviations.DefaultNetworkInstance(dut)

	batchSet := &gnmi.SetBatch{}
	d := oc.Root{}
	tableConn := d.GetOrCreateNetworkInstance(dni).GetOrCreateTableConnection(srcProto, dstProto, addfmly)
	tableConn.SetImportPolicy([]string{policyName})
	if !deviations.SkipSettingDisableMetricPropagation(dut) {
		tableConn.SetDisableMetricPropagation(metricPropagation)
	}
	gnmi.BatchUpdate(batchSet, gnmi.OC().NetworkInstance(dni).TableConnection(srcProto, dstProto, addfmly).Config(), tableConn)

	if addfmly == oc.Types_ADDRESS_FAMILY_IPV4 {
		addfmly = oc.Types_ADDRESS_FAMILY_IPV6
	} else {
		addfmly = oc.Types_ADDRESS_FAMILY_IPV4
	}
	tableConn1 := d.GetOrCreateNetworkInstance(dni).GetOrCreateTableConnection(srcProto, dstProto, addfmly)
	tableConn1.SetImportPolicy([]string{policyName})
	if !deviations.SkipSettingDisableMetricPropagation(dut) {
		tableConn1.SetDisableMetricPropagation(metricPropagation)
	}
	gnmi.BatchUpdate(batchSet, gnmi.OC().NetworkInstance(dni).TableConnection(srcProto, dstProto, addfmly).Config(), tableConn1)

	batchSet.Set(t, dut)
}

func configureRoutePolicy(ipPrefixSet string, prefixSet string,
	rplName string, prefixSubnetRange string, statement string,
	rplType oc.E_RoutingPolicy_PolicyResultType, tagSetName string, tagValue oc.UnionUint32) (*oc.RoutingPolicy, error) {

	d := &oc.Root{}
	rp := d.GetOrCreateRoutingPolicy()

	pdef := rp.GetOrCreatePolicyDefinition(rplName)

	// Condition for prefix set configuration
	if prefixSet != "" && ipPrefixSet != "" && prefixSubnetRange != "" {
		pset := rp.GetOrCreateDefinedSets().GetOrCreatePrefixSet(prefixSet)
		pset.GetOrCreatePrefix(ipPrefixSet, prefixSubnetRange)
	}

	// Create a common statement. This can be adjusted based on unique requirements.
	stmt, err := pdef.AppendNewStatement(statement)
	if err != nil {
		return nil, err
	}
	stmt.GetOrCreateActions().SetPolicyResult(rplType)

	// Condition for tag set configuration
	if tagSetName != "" {
		// Create or get the tag set and set its value.
		tagSet := rp.GetOrCreateDefinedSets().GetOrCreateTagSet(tagSetName)
		tagSet.SetTagValue([]oc.RoutingPolicy_DefinedSets_TagSet_TagValue_Union{tagValue})

		// Assuming conditions specific to tag set need to be set on the common statement.
		stmt.GetOrCreateConditions().GetOrCreateMatchTagSet().SetTagSet(tagSetName)
	}

	return rp, nil
}

func configureStaticRoute(t *testing.T,
	dut *ondatra.DUTDevice,
	ipv4Route string,
	ipv4Mask string,
	tagValueV4 uint32,
	metricValueV4 uint32,
	ipv6Route string,
	ipv6Mask string,
	tagValueV6 uint32,
	metricValueV6 uint32) {

	staticRoute1 := ipv4Route + "/" + ipv4Mask
	staticRoute2 := ipv6Route + "/" + ipv6Mask

	ni := oc.NetworkInstance{Name: ygot.String(deviations.DefaultNetworkInstance(dut))}
	static := ni.GetOrCreateProtocol(oc.PolicyTypes_INSTALL_PROTOCOL_TYPE_STATIC, deviations.StaticProtocolName(dut))
	sr := static.GetOrCreateStatic(staticRoute1)
	sr.SetTag, _ = sr.To_NetworkInstance_Protocol_Static_SetTag_Union(tagValueV4)
	nh := sr.GetOrCreateNextHop("0")
	nh.NextHop = oc.UnionString(isissession.ATEISISAttrs.IPv4)
	nh.Metric = ygot.Uint32(metricValueV4)

	sr2 := static.GetOrCreateStatic(staticRoute2)
	sr2.SetTag, _ = sr.To_NetworkInstance_Protocol_Static_SetTag_Union(tagValueV6)
	nh2 := sr2.GetOrCreateNextHop("0")
	nh2.NextHop = oc.UnionString(isissession.ATEISISAttrs.IPv6)
	nh2.Metric = ygot.Uint32(metricValueV6)

	gnmi.Update(t, dut, gnmi.OC().NetworkInstance(deviations.DefaultNetworkInstance(dut)).Protocol(
		oc.PolicyTypes_INSTALL_PROTOCOL_TYPE_STATIC,
		deviations.StaticProtocolName(dut)).Config(),
		static)
}

func configureOTGFlows(t *testing.T, top gosnappi.Config, ts *isissession.TestSession) {
	t.Helper()

	srcV4 := ts.ATEIntf2.Ethernets().Items()[0].Ipv4Addresses().Items()[0]
	srcV6 := ts.ATEIntf2.Ethernets().Items()[0].Ipv6Addresses().Items()[0]

	dst1V4 := ts.ATEIntf1.Ethernets().Items()[0].Ipv4Addresses().Items()[0]
	dst1V6 := ts.ATEIntf1.Ethernets().Items()[0].Ipv6Addresses().Items()[0]

	v4F := top.Flows().Add()
	v4F.SetName(v4Flow).Metrics().SetEnable(true)
	v4F.TxRx().Device().SetTxNames([]string{srcV4.Name()}).SetRxNames([]string{dst1V4.Name()})

	v4FEth := v4F.Packet().Add().Ethernet()
	v4FEth.Src().SetValue(isissession.ATETrafficAttrs.MAC)

	v4FIp := v4F.Packet().Add().Ipv4()
	v4FIp.Src().SetValue(srcV4.Address())
	v4FIp.Dst().Increment().SetStart(v4TrafficStart).SetCount(254)

	eth := v4F.EgressPacket().Add().Ethernet()
	ethTag := eth.Dst().MetricTags().Add()
	ethTag.SetName("MACTrackingv4").SetOffset(36).SetLength(12)

	v6F := top.Flows().Add()
	v6F.SetName(v6Flow).Metrics().SetEnable(true)
	v6F.TxRx().Device().SetTxNames([]string{srcV6.Name()}).SetRxNames([]string{dst1V6.Name()})

	v6FEth := v6F.Packet().Add().Ethernet()
	v6FEth.Src().SetValue(isissession.ATETrafficAttrs.MAC)

	v6FIP := v6F.Packet().Add().Ipv6()
	v6FIP.Src().SetValue(srcV6.Address())
	v6FIP.Dst().Increment().SetStart(v6TrafficStart).SetCount(1)

	eth = v6F.EgressPacket().Add().Ethernet()
	ethTag = eth.Dst().MetricTags().Add()
	ethTag.SetName("MACTrackingv6").SetOffset(36).SetLength(12)
}

func advertiseRoutesWithISIS(t *testing.T, ts *isissession.TestSession) {
	t.Helper()

	// configure emulated network params
	net2v4 := ts.ATEIntf1.Isis().V4Routes().Add().SetName("v4-isisNet-dev1").SetLinkMetric(10)
	net2v4.Addresses().Add().SetAddress(advertisedIPv4.address).SetPrefix(advertisedIPv4.prefix)
	net2v6 := ts.ATEIntf1.Isis().V6Routes().Add().SetName("v6-isisNet-dev1").SetLinkMetric(10)
	net2v6.Addresses().Add().SetAddress(advertisedIPv6.address).SetPrefix(advertisedIPv6.prefix)
}

func verifyRplConfig(t *testing.T, dut *ondatra.DUTDevice, tagSetName string,
	tagValue oc.UnionUint32) {

	tagSetState := gnmi.Get(t, dut, gnmi.OC().RoutingPolicy().DefinedSets().TagSet(tagSetName).TagValue().State())
	tagNameState := gnmi.Get(t, dut, gnmi.OC().RoutingPolicy().DefinedSets().TagSet(tagSetName).Name().State())

	setTagValue := []oc.RoutingPolicy_DefinedSets_TagSet_TagValue_Union{tagValue}

	for _, value := range tagSetState {
		configuredTagValue := []oc.RoutingPolicy_DefinedSets_TagSet_TagValue_Union{value}
		if setTagValue[0] == configuredTagValue[0] {
			t.Logf("Passed: setTagValue is %v and configuredTagValue is %v", setTagValue[0], configuredTagValue[0])
		} else {
			t.Errorf("Failed: setTagValue is %v and configuredTagValue is %v", setTagValue[0], configuredTagValue[0])
		}
	}
	t.Logf("verify tag name matches expected")
	if tagNameState != tagSetName {
		t.Errorf("Failed to get tag-set name got %s wanted %s", tagNameState, tagSetName)
	} else {
		t.Logf("Passed Found tag-set name got %s wanted %s", tagNameState, tagSetName)
	}
}

func TestStaticToISISRedistribution(t *testing.T) {
	var ts *isissession.TestSession

	t.Run("Initial Setup", func(t *testing.T) {
		t.Run("Configure ISIS on DUT", func(t *testing.T) {
			ts = isissession.MustNew(t).WithISIS()
			if err := ts.PushDUT(context.Background(), t); err != nil {
				t.Fatalf("Unable to push initial DUT config: %v", err)
			}
		})

		t.Run("Configure Static Route on DUT", func(t *testing.T) {
			ipv4Mask := strconv.FormatUint(uint64(v4RoutePrefix), 10)
			ipv6Mask := strconv.FormatUint(uint64(v6RoutePrefix), 10)
			configureStaticRoute(t, ts.DUT, v4Route, ipv4Mask, 40, 104, v6Route, ipv6Mask, 60, 106)
		})

		t.Run("OTG Configuration", func(t *testing.T) {
			configureOTGFlows(t, ts.ATETop, ts)
			advertiseRoutesWithISIS(t, ts)
			ts.PushAndStart(t)
			ts.MustAdjacency(t)

			otgutils.WaitForARP(t, ts.ATE.OTG(), ts.ATETop, "IPv4")
			otgutils.WaitForARP(t, ts.ATE.OTG(), ts.ATETop, "IPv6")
		})
	})

	cases := []struct {
		desc                      string
		policyMetric              string
		policyLevel               string
		policyStmtType            oc.E_RoutingPolicy_PolicyResultType
		metricPropogation         bool
		protoSrc                  oc.E_PolicyTypes_INSTALL_PROTOCOL_TYPE
		protoDst                  oc.E_PolicyTypes_INSTALL_PROTOCOL_TYPE
		protoAf                   oc.E_Types_ADDRESS_FAMILY
		importPolicyConfig        bool
		importPolicyVerify        bool
		defaultImportPolicyConfig bool
		defaultImportPolicyVerify bool
		rplPrefixMatch            string
		PrefixSet                 string
		RplName                   string
		prefixMatchMask           string
		RplStatement              string
		verifyTrafficStats        bool
		trafficFlows              []string
		tagSet                    string
		tagValue                  oc.UnionUint32
	}{{
		desc:                      "RT-2.12.1: Redistribute IPv4 static route to IS-IS with metric propogation diabled",
		metricPropogation:         false,
		protoAf:                   oc.Types_ADDRESS_FAMILY_IPV4,
		defaultImportPolicyConfig: true,
		RplName:                   "DEFAULT-POLICY-PASS-ALL-V4",
		RplStatement:              "PASS-ALL",
		policyStmtType:            oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE,
	}, {
		desc:                      "RT-2.12.2: Redistribute IPv4 static route to IS-IS with metric propogation enabled",
		metricPropogation:         false,
		protoAf:                   oc.Types_ADDRESS_FAMILY_IPV6,
		defaultImportPolicyConfig: true,
		RplName:                   "DEFAULT-POLICY-PASS-ALL-V6",
		RplStatement:              "PASS-ALL",
		policyStmtType:            oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE,
	}, {
		desc:                      "RT-2.12.3: Redistribute IPv6 static route to IS-IS with metric propogation diabled",
		metricPropogation:         true,
		protoAf:                   oc.Types_ADDRESS_FAMILY_IPV4,
		defaultImportPolicyConfig: true,
		RplName:                   "DEFAULT-POLICY-PASS-ALL-V4",
		RplStatement:              "PASS-ALL",
		policyStmtType:            oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE,
	}, {
		desc:                      "RT-2.12.4: Redistribute IPv6 static route to IS-IS with metric propogation enabled",
		metricPropogation:         true,
		protoAf:                   oc.Types_ADDRESS_FAMILY_IPV6,
		defaultImportPolicyConfig: true,
		RplName:                   "DEFAULT-POLICY-PASS-ALL-V6",
		RplStatement:              "PASS-ALL",
		policyStmtType:            oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE,
	}, {
		desc:                      "RT-2.12.5: Redistribute IPv4 and IPv6 static route to IS-IS with default-import-policy set to reject",
		metricPropogation:         false,
		protoAf:                   oc.Types_ADDRESS_FAMILY_IPV4,
		defaultImportPolicyConfig: true,
		RplName:                   "DEFAULT-POLICY-PASS-ALL-V4",
		RplStatement:              "PASS-ALL",
		policyStmtType:            oc.RoutingPolicy_PolicyResultType_REJECT_ROUTE,
	}, {
		desc:               "RT-2.12.6: Redistribute IPv4 static route to IS-IS matching a prefix using a route-policy",
		importPolicyConfig: true,
		protoAf:            oc.Types_ADDRESS_FAMILY_IPV4,
		rplPrefixMatch:     v4Route,
		PrefixSet:          v4PrefixSet,
		RplName:            v4RoutePolicy,
		prefixMatchMask:    prefixMatch,
		RplStatement:       v4Statement,
		metricPropogation:  true,
		policyStmtType:     oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE,
		verifyTrafficStats: true,
		trafficFlows:       []string{v4Flow},
	}, {
		desc:               "RT-2.12.7: Redistribute IPv4 static route to IS-IS matching a tag",
		importPolicyConfig: true,
		protoAf:            oc.Types_ADDRESS_FAMILY_IPV4,
		RplName:            v4RoutePolicy,
		RplStatement:       v4Statement,
		metricPropogation:  true,
		policyStmtType:     oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE,
		verifyTrafficStats: true,
		trafficFlows:       []string{v4Flow},
		tagSet:             "tag-set-v4",
		tagValue:           100,
	}, {
		desc:               "RT-2.12.8: Redistribute IPv6 static route to IS-IS matching a prefix using a route-policy",
		importPolicyConfig: true,
		protoAf:            oc.Types_ADDRESS_FAMILY_IPV6,
		rplPrefixMatch:     v6Route,
		PrefixSet:          v6PrefixSet,
		RplName:            v6RoutePolicy,
		prefixMatchMask:    prefixMatch,
		RplStatement:       v6Statement,
		policyStmtType:     oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE,
		verifyTrafficStats: true,
		trafficFlows:       []string{v6Flow},
	}, {
		desc:               "RT-2.12.9: Redistribute IPv6 static route to IS-IS matching a prefix using a route-policy",
		importPolicyConfig: true,
		protoAf:            oc.Types_ADDRESS_FAMILY_IPV4,
		RplName:            v6RoutePolicy,
		RplStatement:       v6Statement,
		metricPropogation:  true,
		policyStmtType:     oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE,
		verifyTrafficStats: true,
		trafficFlows:       []string{v4Flow},
		tagSet:             "tag-set-v6",
		tagValue:           100,
	}}

	for _, tc := range cases {
		dni := deviations.DefaultNetworkInstance(ts.DUT)

		t.Run(tc.desc, func(t *testing.T) {
			if tc.defaultImportPolicyConfig {
				t.Run(fmt.Sprintf("Config Default Policy Type %s", tc.policyStmtType.String()), func(t *testing.T) {
					rpl, err := configureRoutePolicy(tc.rplPrefixMatch, tc.PrefixSet,
						tc.RplName, tc.prefixMatchMask, tc.RplStatement, tc.policyStmtType, tc.tagSet, tc.tagValue)
					if err != nil {
						fmt.Println("Error configuring route policy:", err)
						return
					}
					gnmi.Update(t, ts.DUT, gnmi.OC().RoutingPolicy().Config(), rpl)
				})

				t.Run(fmt.Sprintf("Attach RPL %v Type %v to ISIS %v", tc.RplName, tc.policyStmtType.String(), dni), func(t *testing.T) {
					isisImportPolicyConfig(t, ts.DUT, tc.RplName, protoSrc, protoDst, tc.protoAf, tc.metricPropogation)
				})

				t.Run(fmt.Sprintf("Verify RPL %v Attributes", tc.RplName), func(t *testing.T) {
					getAndVerifyIsisImportPolicy(t, ts.DUT, tc.metricPropogation, tc.RplName, tc.protoAf.String())
					path := gnmi.OC().NetworkInstance(deviations.DefaultNetworkInstance(ts.DUT)).TableConnection(
						oc.PolicyTypes_INSTALL_PROTOCOL_TYPE_STATIC,
						oc.PolicyTypes_INSTALL_PROTOCOL_TYPE_ISIS,
						oc.Types_ADDRESS_FAMILY_IPV4,
					)

					output := gnmi.Get(t, ts.DUT.GNMIOpts().WithYGNMIOpts(ygnmi.WithUseGet(), ygnmi.WithEncoding(gpb.Encoding_JSON_IETF)), path.State())
					t.Log(output)
				})
			}

			if tc.importPolicyConfig {
				t.Run(fmt.Sprintf("Config Import Policy Type %v", tc.policyStmtType.String()), func(t *testing.T) {

					t.Run(fmt.Sprintf("Config %v Route-Policy", tc.protoAf), func(t *testing.T) {
						rpl, err := configureRoutePolicy(tc.rplPrefixMatch, tc.PrefixSet, tc.RplName,
							tc.prefixMatchMask, tc.RplStatement, tc.policyStmtType, tc.tagSet, tc.tagValue)
						if err != nil {
							t.Fatalf("Failed to configure Route Policy: %v", err)
						}
						gnmi.Update(t, ts.DUT, gnmi.OC().RoutingPolicy().Config(), rpl)

						if tc.tagSet != "" {
							t.Run(fmt.Sprintf("Verify Configuration for RPL %v value %v",
								tc.tagSet, tc.tagValue), func(t *testing.T) {
								verifyRplConfig(t, ts.DUT, tc.tagSet, tc.tagValue)
							})
						}

					})
					t.Run(fmt.Sprintf("Attach RPL %v To ISIS", tc.RplName), func(t *testing.T) {
						isisImportPolicyConfig(t, ts.DUT, tc.RplName, protoSrc, protoDst, tc.protoAf, tc.metricPropogation)
					})

					t.Run(fmt.Sprintf("Verify RPL %v Attributes", tc.RplName), func(t *testing.T) {
						getAndVerifyIsisImportPolicy(t, ts.DUT, tc.metricPropogation, tc.RplName, tc.protoAf.String())

						path := gnmi.OC().NetworkInstance(deviations.DefaultNetworkInstance(ts.DUT)).TableConnection(
							oc.PolicyTypes_INSTALL_PROTOCOL_TYPE_STATIC,
							oc.PolicyTypes_INSTALL_PROTOCOL_TYPE_ISIS,
							oc.Types_ADDRESS_FAMILY_IPV4)

						output := gnmi.LookupConfig(t, ts.DUT.GNMIOpts().WithYGNMIOpts(ygnmi.WithUseGet(), ygnmi.WithEncoding(gpb.Encoding_JSON_IETF)), path.Config())
						t.Log(output)
					})

				})
			}

			if tc.verifyTrafficStats {
				t.Run(fmt.Sprintf("Verify traffic for %s", tc.trafficFlows), func(t *testing.T) {

					ts.ATE.OTG().StartTraffic(t)
					time.Sleep(trafficDuration)
					ts.ATE.OTG().StopTraffic(t)

					for _, flow := range tc.trafficFlows {
						loss := otgutils.GetFlowLossPct(t, ts.ATE.OTG(), flow, 20*time.Second)
						if loss > lossTolerance {
							t.Errorf("Traffic loss too high for flow %s", flow)
						} else {
							t.Logf("Traffic loss for flow %s is %v", flow, loss)
						}
					}
				})

			}

			t.Run("Verify Route on OTG", func(t *testing.T) {
				// TODO: Verify routes are learned on the ATE device. This is pending a fix from IXIA and OTG
				// TODO: https://github.com/open-traffic-generator/fp-testbed-cisco/issues/10#issuecomment-2015756900
				t.Skip("Skipping this due to OTG issue not learning routes.")

				configuredMetric := uint32(100)
				_, ok := gnmi.WatchAll(t, ts.ATE.OTG(), gnmi.OTG().IsisRouter("devIsis").LinkStateDatabase().LspsAny().Tlvs().ExtendedIpv4Reachability().PrefixAny().Metric().State(), time.Minute, func(v *ygnmi.Value[uint32]) bool {
					metric, present := v.Val()
					if present {
						if metric == configuredMetric {
							return true
						}
					}
					return false
				}).Await(t)

				metricInReceivedLsp := gnmi.GetAll(t, ts.ATE.OTG(), gnmi.OTG().IsisRouter("devIsis").LinkStateDatabase().LspsAny().Tlvs().ExtendedIpv4Reachability().PrefixAny().Metric().State())[0]
				if !ok {
					t.Fatalf("Metric not matched. Expected %d got %d ", configuredMetric, metricInReceivedLsp)
				}
			})
		})
	}
}