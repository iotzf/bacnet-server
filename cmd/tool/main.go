package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iotzf/bacnet-server/internal/model"
	"github.com/iotzf/bacnet-server/internal/protocol"
)

// simulateDataChanges 模拟设备数据变化
func simulateDataChanges(server *protocol.BACnetServer) {
	// 初始化随机数生成器
	rand.Seed(time.Now().UnixNano())

	fmt.Println("数据模拟任务已启动，将每5秒模拟一次数据变化...")

	// 定期模拟数据变化
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 模拟温度传感器数据变化（18-30°C范围内随机变化）
			newTemp := 18.0 + rand.Float64()*12.0
			server.SimulateDataChange(1, model.PropertyIdentifierPresentValue, newTemp)

			// 模拟湿度传感器数据变化（30-80%范围内随机变化）
			newHumidity := 30.0 + rand.Float64()*50.0
			server.SimulateDataChange(2, model.PropertyIdentifierPresentValue, newHumidity)

			// 模拟压力传感器数据变化（3.0-6.0 bar范围内随机变化）
			newPressure := 3.0 + rand.Float64()*3.0
			server.SimulateDataChange(3, model.PropertyIdentifierPresentValue, newPressure)
		}
	}
}

func main() {
	// 定义命令行参数
	port := flag.Int("port", 47808, "Port to listen on for BACnet messages")
	deviceID := flag.Uint("device-id", 1001, "Device instance number")
	deviceName := flag.String("device-name", "Go BACnet Server", "Name of the BACnet device")
	location := flag.String("location", "Test Location", "Physical location of the device")
	flag.Parse()

	// 创建BACnet设备
	device := model.NewDevice(uint32(*deviceID), *deviceName, *location)

	// 添加一些示例对象
	addSampleObjects(device)

	// 创建并启动BACnet服务器
	server, err := protocol.NewBACnetServer(device, fmt.Sprintf(":%d", *port))
	if err != nil {
		fmt.Printf("Failed to create BACnet server: %v\n", err)
		os.Exit(1)
	}

	// 启动服务器
	server.Start()

	// 启动数据模拟任务
	//go simulateDataChanges(server)

	// 设置信号处理以便优雅关闭
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 等待终止信号
	<-sigChan

	// 关闭服务器
	server.Stop()
	fmt.Println("Program terminated")
}

// addSampleObjects 向设备添加示例对象
func addSampleObjects(device *model.Device) {
	// 添加模拟输入对象 (温度传感器)
	tempSensor := model.NewBACnetObject(model.ObjectTypeAnalogInput, 1, "Temperature Sensor")
	tempSensor.WriteProperty(model.PropertyIdentifierDescription, "Room temperature sensor")
	tempSensor.WriteProperty(model.PropertyIdentifierPresentValue, 22.5) // 22.5°C
	device.AddObject(tempSensor)

	// 添加模拟输入对象 (湿度传感器)
	humiditySensor := model.NewBACnetObject(model.ObjectTypeAnalogInput, 2, "Humidity Sensor")
	humiditySensor.WriteProperty(model.PropertyIdentifierDescription, "Room humidity sensor")
	humiditySensor.WriteProperty(model.PropertyIdentifierPresentValue, 45.0) // 45%
	device.AddObject(humiditySensor)

	// 添加二进制输出对象 (灯光控制)
	lightSwitch := model.NewBACnetObject(model.ObjectTypeBinaryOutput, 1, "Light Switch")
	lightSwitch.WriteProperty(model.PropertyIdentifierDescription, "Main room light")
	lightSwitch.WriteProperty(model.PropertyIdentifierPresentValue, false) // 关闭状态
	device.AddObject(lightSwitch)

	// 添加二进制输出对象 (空调控制)
	acSwitch := model.NewBACnetObject(model.ObjectTypeBinaryOutput, 2, "AC Switch")
	acSwitch.WriteProperty(model.PropertyIdentifierDescription, "Air conditioner control")
	acSwitch.WriteProperty(model.PropertyIdentifierPresentValue, true) // 开启状态
	device.AddObject(acSwitch)

	// 添加模拟值对象 (设定温度)
	setpoint := model.NewBACnetObject(model.ObjectTypeAnalogValue, 1, "Temperature Setpoint")
	setpoint.WriteProperty(model.PropertyIdentifierDescription, "Desired room temperature")
	setpoint.WriteProperty(model.PropertyIdentifierPresentValue, 22.0) // 22.0°C
	device.AddObject(setpoint)

	// 添加支持告警的压力传感器
	pressureSensor := model.NewBACnetObject(model.ObjectTypeAnalogInput, 3, "Pressure Sensor with Alarm")
	pressureSensor.WriteProperty(model.PropertyIdentifierDescription, "Water pressure sensor with alarm capability")
	pressureSensor.WriteProperty(model.PropertyIdentifierPresentValue, 4.5) // 4.5 bar
	pressureSensor.SetEventState(model.EventStateNormal)
	pressureSensor.SetNotificationClass(1)
	pressureSensor.SetStatusFlags(0) // 无标志
	device.AddObject(pressureSensor)

	// 添加通知类对象
	notificationClass := model.NewBACnetObject(model.ObjectTypeNotificationClass, 1, "Default Notification Class")
	notificationClass.WriteProperty(model.PropertyIdentifierDescription, "Default notification settings")
	notificationClass.WriteProperty(model.PropertyIdentifierPriority, 10) // 中等优先级
	device.AddObject(notificationClass)

	// 添加事件日志对象
	eventLog := model.NewBACnetObject(model.ObjectTypeEventLog, 1, "System Event Log")
	eventLog.WriteProperty(model.PropertyIdentifierDescription, "System-wide event log")
	device.AddObject(eventLog)

	// 添加文件对象 (配置文件)
	configFile := model.NewBACnetFile(1, "Configuration File", model.FileAccessMethodStream)
	device.AddObject(configFile.BACnetObject)

	// 添加事件注册对象
	eventEnrollment := model.NewBACnetObject(model.ObjectTypeEventEnrollment, 1, "Pressure Alarm Enrollment")
	eventEnrollment.WriteProperty(model.PropertyIdentifierDescription, "Enrollment for pressure alarm events")
	device.AddObject(eventEnrollment)

	fmt.Println("Added sample objects:")
	fmt.Println("  - Temperature Sensor (AI-1)")
	fmt.Println("  - Humidity Sensor (AI-2)")
	fmt.Println("  - Pressure Sensor with Alarm (AI-3)")
	fmt.Println("  - Light Switch (BO-1)")
	fmt.Println("  - AC Switch (BO-2)")
	fmt.Println("  - Temperature Setpoint (AV-1)")
	fmt.Println("  - Default Notification Class (NC-1)")
	fmt.Println("  - System Event Log (EL-1)")
	fmt.Println("  - Configuration File (File-1)")
	fmt.Println("  - Pressure Alarm Enrollment (EE-1)")
}
