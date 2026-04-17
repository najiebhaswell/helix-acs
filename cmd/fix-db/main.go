package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type WANInfo struct {
	ConnectionType string `bson:"connection_type" json:"connection_type"`
	ServiceType    string `bson:"service_type"    json:"service_type"`
	IPAddress      string `bson:"ip_address"      json:"ip_address"`
	SubnetMask     string `bson:"subnet_mask"     json:"subnet_mask"`
	Gateway        string `bson:"gateway"         json:"gateway"`
	DNS1           string `bson:"dns1"            json:"dns1"`
	DNS2           string `bson:"dns2"            json:"dns2"`
	MACAddress     string `bson:"mac_address"     json:"mac_address"`
	PPPoEUsername  string `bson:"pppoe_username"  json:"pppoe_username"`
	MTU            int    `bson:"mtu"             json:"mtu"`
	LinkStatus     string `bson:"link_status"     json:"link_status"`
	UptimeSeconds  int64  `bson:"uptime_seconds"  json:"uptime_seconds"`
}

func samePrefix(a, b string) bool {
	aParts := strings.SplitN(a, ".", 3)
	bParts := strings.SplitN(b, ".", 3)
	if len(aParts) < 2 || len(bParts) < 2 {
		return false
	}
	return aParts[0] == bParts[0] && aParts[1] == bParts[1]
}

func findGateway(wanIface string, wanIP string, params map[string]string) string {
	gateway := ""
	if wanIface != "" {
		for k, v := range params {
			if strings.HasSuffix(k, ".Interface") && v == wanIface && strings.Contains(k, "IPv4Forwarding") {
				base := strings.TrimSuffix(k, ".Interface")
				enabled := params[base+".Enable"]
				if enabled == "1" || enabled == "true" || enabled == "" {
					if gw := params[base+".GatewayIPAddress"]; gw != "" && gw != "0.0.0.0" {
						gateway = gw
						break
					}
				}
			}
		}
	}
	if gateway == "" {
		for k, v := range params {
			if !strings.HasSuffix(k, ".GatewayIPAddress") || !strings.Contains(k, "IPv4Forwarding") {
				continue
			}
			if v == "" || v == "0.0.0.0" {
				continue
			}
			if wanIP != "" && samePrefix(wanIP, v) {
				gateway = v
				break
			}
			if gateway == "" {
				gateway = v
			}
		}
	}
	return gateway
}

func main() {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		panic(err)
	}
	defer client.Disconnect(context.Background())

	col := client.Database("helix_acs").Collection("devices")

	var dev bson.M
	err = col.FindOne(context.Background(), bson.M{"serial": "225C1CU010292"}).Decode(&dev)
	if err != nil {
		panic(err)
	}

	paramsObj := dev["parameters"].(bson.M)
	params := make(map[string]string)
	for k, v := range paramsObj {
		if str, ok := v.(string); ok {
			params[k] = str
		}
	}

	var wans []WANInfo
	macAddress := params["Device.Ethernet.Interface.1.MACAddress"]

	for k, v := range params {
		m := regexp.MustCompile(`^Device\.IP\.Interface\.(\d+)\.X_TP_ConnType$`).FindStringSubmatch(k)
		if m != nil && v != "LAN" && v != "Bridge" {
			idx := m[1]
			ipAddr := params[fmt.Sprintf("Device.IP.Interface.%s.IPv4Address.1.IPAddress", idx)]
			if ipAddr == "" || ipAddr == "0.0.0.0" {
				continue
			}

			mtu, _ := strconv.Atoi(params[fmt.Sprintf("Device.IP.Interface.%s.MaxMTUSize", idx)])

			wan := WANInfo{
				ConnectionType: v,
				ServiceType:    params[fmt.Sprintf("Device.IP.Interface.%s.X_TP_ServiceType", idx)],
				IPAddress:      ipAddr,
				SubnetMask:     params[fmt.Sprintf("Device.IP.Interface.%s.IPv4Address.1.SubnetMask", idx)],
				LinkStatus:     params[fmt.Sprintf("Device.IP.Interface.%s.Status", idx)],
				Gateway:        findGateway(fmt.Sprintf("Device.IP.Interface.%s.", idx), ipAddr, params),
				MACAddress:     macAddress,
				MTU:            mtu,
			}

			uptimeStr := params[fmt.Sprintf("Device.IP.Interface.%s.X_TP_Uptime", idx)]
			if uptimeStr == "" {
				uptimeStr = params[fmt.Sprintf("Device.IP.Interface.%s.LastChange", idx)]
			}
			if uptimeStr != "" {
				if u, err := strconv.ParseInt(uptimeStr, 10, 64); err == nil {
					wan.UptimeSeconds = u
				}
			}

			if v == "PPPoE" {
				lower := params[fmt.Sprintf("Device.IP.Interface.%s.LowerLayers", idx)]
				if pppMatch := regexp.MustCompile(`Device\.PPP\.Interface\.(\d+)`).FindStringSubmatch(lower); pppMatch != nil {
					pppIdx := pppMatch[1]
					wan.PPPoEUsername = params[fmt.Sprintf("Device.PPP.Interface.%s.Username", pppIdx)]

					dnsStr := params[fmt.Sprintf("Device.PPP.Interface.%s.IPCP.DNSServers", pppIdx)]
					if dnsStr != "" {
						parts := strings.Split(dnsStr, ",")
						if len(parts) > 0 {
							wan.DNS1 = strings.TrimSpace(parts[0])
						}
						if len(parts) > 1 {
							wan.DNS2 = strings.TrimSpace(parts[1])
						}
					}
				}
			} else if v == "DHCP" {
				wanIfacePath := fmt.Sprintf("Device.IP.Interface.%s.", idx)
				for pk, pval := range params {
					if strings.HasSuffix(pk, ".Interface") && strings.HasPrefix(pk, "Device.DHCPv4.Client.") && pval == wanIfacePath {
						base := strings.TrimSuffix(pk, ".Interface")
						dnsStr := params[base+".DNSServers"]
						if dnsStr != "" {
							parts := strings.Split(dnsStr, ",")
							if len(parts) > 0 {
								wan.DNS1 = strings.TrimSpace(parts[0])
							}
							if len(parts) > 1 {
								wan.DNS2 = strings.TrimSpace(parts[1])
							}
						}
						break
					}
				}
			}

			wans = append(wans, wan)
		}
	}

	fmt.Printf("Extracted %d WANs\n", len(wans))
	_, err = col.UpdateOne(context.Background(), bson.M{"serial": "225C1CU010292"}, bson.M{"$set": bson.M{"wans": wans}})
	if err != nil {
		panic(err)
	}
	fmt.Println("DB updated successfully!")
}
