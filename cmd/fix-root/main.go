package main

import (
	"context"
	"fmt"
	"strconv"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

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

	uptime, _ := strconv.ParseInt(params["Device.DeviceInfo.UpTime"], 10, 64)
	ramFree, _ := strconv.ParseInt(params["Device.DeviceInfo.MemoryStatus.Free"], 10, 64)
	ramTotal, _ := strconv.ParseInt(params["Device.DeviceInfo.MemoryStatus.Total"], 10, 64)
	cpuUsage, _ := strconv.ParseInt(params["Device.DeviceInfo.ProcessStatus.CPUUsage"], 10, 64)
	acsUrl := params["Device.ManagementServer.URL"]
	lanIp := params["Device.IP.Interface.1.IPv4Address.1.IPAddress"]
	wanIp := params["Device.IP.Interface.5.IPv4Address.1.IPAddress"]

	_, err = col.UpdateOne(context.Background(), bson.M{"serial": "225C1CU010292"}, bson.M{"$set": bson.M{
		"uptime_seconds": uptime,
		"ram_free":       ramFree,
		"ram_total":      ramTotal,
		"cpu_usage":      cpuUsage,
		"acs_url":        acsUrl,
		"ip_address":     lanIp,
		"wan_ip":         wanIp,
	}})
	if err != nil {
		panic(err)
	}
	fmt.Println("Root fields updated successfully!")
}
