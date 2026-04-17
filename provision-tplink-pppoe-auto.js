// GenieACS Provision Script: Auto PPPoE untuk semua TP-Link ONT
// Menggunakan ext() untuk lookup credentials berdasarkan serial number
// Script ini TIDAK mengubah provision-tplink-pppoe.js yang sudah berjalan
//
// Cara kerja:
//   1. Ambil serial number dari DeviceID
//   2. Panggil ext("pppoe-credentials", "lookup", serial) untuk dapat credentials
//   3. Jika ditemukan → jalankan provisioning PPPoE penuh
//   4. Jika tidak ditemukan → skip (ONT belum terdaftar di database)

// ============================================================
// STEP 1: Ambil serial number
// ============================================================
let serial = declare("DeviceID.SerialNumber", { value: 1 });
let serialNumber = serial.value ? serial.value[0] : "";

if (!serialNumber) {
    log("AUTO-PPPOE: Serial number tidak ditemukan. Skip.");
} else {
    // ============================================================
    // STEP 2: Lookup credentials dari ext script
    // ============================================================
    let creds = ext("pppoe-credentials", "lookup", serialNumber);

    if (!creds) {
        log("AUTO-PPPOE: Serial " + serialNumber + " tidak terdaftar di database. Skip.");
    } else {
        let vlanId = creds[0];
        let pppoeUser = creds[1];
        let pppoePass = creds[2];

        log("AUTO-PPPOE: Serial " + serialNumber + " ditemukan. VLAN=" + vlanId + " User=" + pppoeUser);

        // ============================================================
        // STEP 3: Cek apakah PPPoE sudah terkonfigurasi dengan benar
        // Jika sudah ada → skip (idempotent)
        // ============================================================
        let alreadyConfigured = false;
        for (let n of declare("Device.PPP.Interface.*.Username", { value: 1 })) {
            if (n.value && n.value[0] == pppoeUser) {
                for (let ip of declare("Device.IP.Interface.*.X_TP_ConnName", { value: 1 })) {
                    if (ip.value && ip.value[0] == "Internet_PPPoE") {
                        alreadyConfigured = true;
                        break;
                    }
                }
                break;
            }
        }

        if (alreadyConfigured) {
            log("AUTO-PPPOE: PPPoE sudah terkonfigurasi untuk " + pppoeUser + ". Skip.");
            // Refresh data saja
            declare("Device.PPP.Interface.*", { path: Date.now() });
            declare("Device.IP.Interface.*", { path: Date.now() });
        } else {
            log("AUTO-PPPOE: PPPoE belum ada. Mulai provisioning penuh...");

            // ============================================================
            // PROVISIONING PENUH — Layer-by-layer (sama dengan provision-tplink-pppoe.js)
            // ============================================================

            // LANGKAH 1: GPON Link
            declare("Device.X_TP_GPON.Link.[Alias:pppoe_gpon_" + vlanId + "]", { path: 1 }, { path: 1 });
            declare("Device.X_TP_GPON.Link.[Alias:pppoe_gpon_" + vlanId + "].LowerLayers", { value: 1 }, { value: "Device.Optical.Interface.1." });
            declare("Device.X_TP_GPON.Link.[Alias:pppoe_gpon_" + vlanId + "].Enable", { value: 1 }, { value: true });
            commit();

            let gponPath = "";
            for (let n of declare("Device.X_TP_GPON.Link.*.Alias", { value: 1 })) {
                if (n.value && n.value[0] == "pppoe_gpon_" + vlanId) {
                    gponPath = "Device.X_TP_GPON.Link." + n.path.split(".")[3] + ".";
                    break;
                }
            }
            if (!gponPath) {
                for (let n of declare("Device.X_TP_GPON.Link.*.LowerLayers", { value: 1 })) {
                    if (n.value && n.value[0] == "Device.Optical.Interface.1.") {
                        gponPath = "Device.X_TP_GPON.Link." + n.path.split(".")[3] + ".";
                    }
                }
            }
            log("GPON Path: " + gponPath);

            // LANGKAH 2: Ethernet Link
            declare("Device.Ethernet.Link.[Alias:pppoe_eth_" + vlanId + "]", { path: 1 }, { path: 1 });
            declare("Device.Ethernet.Link.[Alias:pppoe_eth_" + vlanId + "].LowerLayers", { value: 1 }, { value: gponPath });
            declare("Device.Ethernet.Link.[Alias:pppoe_eth_" + vlanId + "].Enable", { value: 1 }, { value: true });
            commit();

            let ethPath = "";
            for (let n of declare("Device.Ethernet.Link.*.Alias", { value: 1 })) {
                if (n.value && n.value[0] == "pppoe_eth_" + vlanId) {
                    ethPath = "Device.Ethernet.Link." + n.path.split(".")[3] + ".";
                    break;
                }
            }
            if (!ethPath) {
                for (let n of declare("Device.Ethernet.Link.*.LowerLayers", { value: 1 })) {
                    if (n.value && n.value[0] == gponPath) {
                        ethPath = "Device.Ethernet.Link." + n.path.split(".")[3] + ".";
                        break;
                    }
                }
            }
            log("Ethernet Link Path: " + ethPath);

            // LANGKAH 3: VLAN Termination
            declare("Device.Ethernet.VLANTermination.[Alias:pppoe_vlan_" + vlanId + "]", { path: 1 }, { path: 1 });
            declare("Device.Ethernet.VLANTermination.[Alias:pppoe_vlan_" + vlanId + "].LowerLayers", { value: 1 }, { value: ethPath });
            declare("Device.Ethernet.VLANTermination.[Alias:pppoe_vlan_" + vlanId + "].VLANID", { value: 1 }, { value: parseInt(vlanId) });
            declare("Device.Ethernet.VLANTermination.[Alias:pppoe_vlan_" + vlanId + "].X_TP_VLANEnable", { value: 1 }, { value: true });
            declare("Device.Ethernet.VLANTermination.[Alias:pppoe_vlan_" + vlanId + "].X_TP_VLANMode", { value: 1 }, { value: 2 });
            declare("Device.Ethernet.VLANTermination.[Alias:pppoe_vlan_" + vlanId + "].X_TP_MulticastStatus", { value: 1 }, { value: 1 });
            declare("Device.Ethernet.VLANTermination.[Alias:pppoe_vlan_" + vlanId + "].Enable", { value: 1 }, { value: true });
            commit();

            let vlanPath = "";
            for (let n of declare("Device.Ethernet.VLANTermination.*.VLANID", { value: 1 })) {
                if (n.value && n.value[0] == parseInt(vlanId)) {
                    vlanPath = "Device.Ethernet.VLANTermination." + n.path.split(".")[3] + ".";
                    break;
                }
            }
            log("VLAN Path: " + vlanPath);

            // LANGKAH 4: PPP Interface
            declare("Device.PPP.Interface.[Alias:Internet_PPPoE]", { path: 1 }, { path: 1 });
            declare("Device.PPP.Interface.[Alias:Internet_PPPoE].LowerLayers", { value: 1 }, { value: vlanPath });
            declare("Device.PPP.Interface.[Alias:Internet_PPPoE].Username", { value: 1 }, { value: pppoeUser });
            declare("Device.PPP.Interface.[Alias:Internet_PPPoE].Password", { value: 1 }, { value: pppoePass });
            declare("Device.PPP.Interface.[Alias:Internet_PPPoE].X_TP_UsernameDomainEnable", { value: 1 }, { value: 1 });
            declare("Device.PPP.Interface.[Alias:Internet_PPPoE].AuthenticationProtocol", { value: 1 }, { value: "AUTO_AUTH" });
            declare("Device.PPP.Interface.[Alias:Internet_PPPoE].Enable", { value: 1 }, { value: true });
            commit();

            let pppPath = "";
            for (let n of declare("Device.PPP.Interface.*.Alias", { value: 1 })) {
                if (n.value && n.value[0] == "Internet_PPPoE") {
                    pppPath = "Device.PPP.Interface." + n.path.split(".")[3] + ".";
                    break;
                }
            }
            if (!pppPath) {
                for (let n of declare("Device.PPP.Interface.*.Username", { value: 1 })) {
                    if (n.value && n.value[0] == pppoeUser) {
                        pppPath = "Device.PPP.Interface." + n.path.split(".")[3] + ".";
                        break;
                    }
                }
            }
            log("PPP Path: " + pppPath);

            // LANGKAH 5: IP Interface
            declare("Device.IP.Interface.[Alias:Internet_IP]", { path: 1 }, { path: 1 });
            declare("Device.IP.Interface.[Alias:Internet_IP].LowerLayers", { value: 1 }, { value: pppPath });
            declare("Device.IP.Interface.[Alias:Internet_IP].IPv4Enable", { value: 1 }, { value: true });
            declare("Device.IP.Interface.[Alias:Internet_IP].X_TP_ConnType", { value: 1 }, { value: "PPPoE" });
            declare("Device.IP.Interface.[Alias:Internet_IP].X_TP_ServiceType", { value: 1 }, { value: "Internet" });
            declare("Device.IP.Interface.[Alias:Internet_IP].X_TP_ConnName", { value: 1 }, { value: "Internet_PPPoE" });
            declare("Device.IP.Interface.[Alias:Internet_IP].MaxMTUSize", { value: 1 }, { value: 1492 });
            declare("Device.IP.Interface.[Alias:Internet_IP].Enable", { value: 1 }, { value: true });
            commit();

            let ipPath = "";
            for (let n of declare("Device.IP.Interface.*.X_TP_ConnName", { value: 1 })) {
                if (n.value && n.value[0] == "Internet_PPPoE") {
                    ipPath = "Device.IP.Interface." + n.path.split(".")[3] + ".";
                    break;
                }
            }
            log("IP Path: " + ipPath);

            // LANGKAH 6: NAT
            declare("Device.NAT.InterfaceSetting.[Alias:NAT_Internet]", { path: 1 }, { path: 1 });
            declare("Device.NAT.InterfaceSetting.[Alias:NAT_Internet].Interface", { value: 1 }, { value: ipPath });
            declare("Device.NAT.InterfaceSetting.[Alias:NAT_Internet].Enable", { value: 1 }, { value: true });

            // LANGKAH 7: Default Gateway (TP-Link Proprietary)
            declare("Device.X_TP_DefaultGateway.IPv4DefaultGatewayType", { value: 1 }, { value: "Manual" });
            declare("Device.X_TP_DefaultGateway.CustomIPv4DefaultGateway", { value: 1 }, { value: "Internet_PPPoE" });
            declare("Device.X_TP_DefaultGateway.IPv6DefaultGatewayType", { value: 1 }, { value: "Manual" });
            declare("Device.X_TP_DefaultGateway.CustomIPv6DefaultGateway", { value: 1 }, { value: "Internet_PPPoE" });

            // LANGKAH 7b: Pastikan hanya ada 1 interface dengan ServiceType Internet
            // Hapus 'Internet' dari interface lain untuk menghindari Multiple Default Gateway
            for (let n of declare("Device.IP.Interface.*.X_TP_ServiceType", { value: 1 })) {
                if (n.value) {
                    let idx = n.path.split(".")[3];
                    let currentService = String(n.value[0]);
                    
                    // Cek apakah ini interface PPPoE kita
                    let isOurs = false;
                    for (let c of declare("Device.IP.Interface." + idx + ".X_TP_ConnName", { value: 1 })) {
                        if (c.value && c.value[0] == "Internet_PPPoE") {
                            isOurs = true;
                        }
                    }

                    if (!isOurs && currentService.indexOf("Internet") >= 0) {
                        // Jika ada TR069, jadikan TR069 saja. Jika tidak, jadikan Other.
                        let newService = (currentService.indexOf("TR069") >= 0) ? "TR069" : "Other";
                        declare("Device.IP.Interface." + idx + ".X_TP_ServiceType", { value: 1 }, { value: newService });
                        
                        // Disable interface jika murni Other (hanya Internet awalnya)
                        if (newService === "Other") {
                            declare("Device.IP.Interface." + idx + ".Enable", { value: 1 }, { value: false });
                            declare("Device.IP.Interface." + idx + ".IPv4Enable", { value: 1 }, { value: false });
                        }
                    }
                }
            }

            // LANGKAH 8: IPv6
            declare(ipPath + "IPv6Enable", { value: 1 }, { value: true });
            declare(ipPath + "X_TP_IPv6AddrType", { value: 1 }, { value: "Auto" });
            commit();

            // LANGKAH 9: DHCPv6
            declare("Device.DHCPv6.Client.[Alias:dhcpv6_pppoe]", { path: 1 }, { path: 1 });
            declare("Device.DHCPv6.Client.[Alias:dhcpv6_pppoe].Interface", { value: 1 }, { value: ipPath });
            declare("Device.DHCPv6.Client.[Alias:dhcpv6_pppoe].RequestAddresses", { value: 1 }, { value: false });
            declare("Device.DHCPv6.Client.[Alias:dhcpv6_pppoe].RequestPrefixes", { value: 1 }, { value: true });
            declare("Device.DHCPv6.Client.[Alias:dhcpv6_pppoe].RequestedOptions", { value: 1 }, { value: "23" });
            declare("Device.DHCPv6.Client.[Alias:dhcpv6_pppoe].X_TP_EnableRaRouter", { value: 1 }, { value: true });
            declare("Device.DHCPv6.Client.[Alias:dhcpv6_pppoe].X_TP_EnableSLAAC", { value: 1 }, { value: true });
            declare("Device.DHCPv6.Client.[Alias:dhcpv6_pppoe].Enable", { value: 1 }, { value: true });

            log("AUTO-PPPOE: === SELESAI Provisioning PPPoE untuk " + serialNumber + " (IPv4+IPv6) ===");

            // Refresh tree
            declare("Device.*", { path: Date.now() });
        }
    }
}
