package model

import (
	"fmt"
	"time"
)

// ObjectType 表示BACnet中的对象类型
type ObjectType uint8

// 常用的BACnet对象类型
const (
	ObjectTypeAnalogInput ObjectType = iota + 1
	ObjectTypeAnalogOutput
	ObjectTypeAnalogValue
	ObjectTypeBinaryInput
	ObjectTypeBinaryOutput
	ObjectTypeBinaryValue
	ObjectTypeDevice
	ObjectTypeTrendLog
	ObjectTypeSchedule
	ObjectTypeMultiStateInput
	ObjectTypeMultiStateOutput
	ObjectTypeFile
	ObjectTypeNotificationClass
	ObjectTypeEventLog
	ObjectTypeEventEnrollment
)

// PropertyIdentifier 表示BACnet中的属性标识符
type PropertyIdentifier uint32

// 常用的BACnet属性标识符
const (
	PropertyIdentifierObjectIdentifier PropertyIdentifier = iota + 1
	PropertyIdentifierObjectType
	PropertyIdentifierObjectName
	PropertyIdentifierPresentValue
	PropertyIdentifierDescription
	PropertyIdentifierDeviceType
	PropertyIdentifierManufacturerName
	PropertyIdentifierModelName
	PropertyIdentifierFirmwareRevision
	PropertyIdentifierApplicationSoftwareVersion
	PropertyIdentifierLocation
	PropertyIdentifierNumberOfApduRetries
	PropertyIdentifierSegmentationSupported
	PropertyIdentifierApdutimeout
	// 告警和事件相关属性
	PropertyIdentifierEventState
	PropertyIdentifierOutOfService
	PropertyIdentifierNotificationClass
	PropertyIdentifierAlarmValue
	PropertyIdentifierAcknowledgedTransitions
	PropertyIdentifierNotifyType
	PropertyIdentifierEventDetectionEnable
	PropertyIdentifierAckedTransitions
	PropertyIdentifierEventTimeStamps
	PropertyIdentifierTimeOfStateChange
	PropertyIdentifierTimeOfLastStateChange
	PropertyIdentifierStatusFlags
	// 文件服务相关属性
	PropertyIdentifierFileSize
	PropertyIdentifierFileAccessMethod
	PropertyIdentifierFileOpeningTag
	PropertyIdentifierFileClosingTag
	// 优先级属性
	PropertyIdentifierPriority
)

// 告警状态枚举
type EventState uint8

const (
	EventStateNormal EventState = iota
	EventStateFault
	EventStateOffNormal
	EventStateHighLimit
	EventStateLowLimit
)

// 通知类型枚举
type NotifyType uint8

const (
	NotifyTypeAlarm NotifyType = iota
	NotifyTypeEvent
	NotifyTypeBoth
)

// 状态标志位
const (
	StatusFlagInAlarm = 1 << iota
	StatusFlagFault
	StatusFlagOverridden
	StatusFlagOutOfService
)

// EventTransition 表示事件转换状态
type EventTransition uint8

const (
	EventTransitionToNormal EventTransition = iota
	EventTransitionToFault
	EventTransitionToOffNormal
	EventTransitionToHighLimit
	EventTransitionToLowLimit
)

// 文件访问方法枚举
type FileAccessMethod uint8

const (
	FileAccessMethodStream FileAccessMethod = iota
	FileAccessMethodRecord
)

// BACnetEvent 表示BACnet事件
type BACnetEvent struct {
	EventType         ObjectType
	EventState        EventState
	TimeStamp         time.Time // 时间戳类型，表示事件发生的实际时间
	MessageText       string
	NotificationClass uint32
}

// COVSubscription 表示变化通知订阅
type COVSubscription struct {
	SubscriptionID                 uint32               // 变化通知订阅ID
	DeviceID                       uint32               // 设备ID
	ObjectIdentifier               ObjectIdentifier     // 对象标识符
	Lifetime                       uint32               // 订阅有效期（秒）
	IssueConfirmedCOVNotifications bool                 // 是否确认发送变化通知
	MonitoredProperties            []PropertyIdentifier // 监控的属性列表
	Timestamp                      time.Time            // 订阅创建时间戳
	ClientAddress                  string               // 客户端IP地址和端口，格式: "192.168.1.1:1234"
}

// BACnetFile 表示BACnet文件对象
type BACnetFile struct {
	*BACnetObject
	FileData     []byte
	AccessMethod FileAccessMethod
	OpeningTag   string
	ClosingTag   string
}

// Alarmable 定义可告警对象接口
type Alarmable interface {
	Object
	GetEventState() EventState
	SetEventState(state EventState)
	GetNotificationClass() uint32
	SetNotificationClass(class uint32)
	GetStatusFlags() uint8
	SetStatusFlags(flags uint8)
	GenerateEvent(state EventState, message string)
}

// ObjectIdentifier 表示BACnet对象标识符
type ObjectIdentifier struct {
	Type     ObjectType
	Instance uint32
}

// Object 定义BACnet对象的接口
type Object interface {
	GetObjectIdentifier() ObjectIdentifier
	GetObjectName() string
	GetObjectType() ObjectType
	ReadProperty(prop PropertyIdentifier) (interface{}, error)
	WriteProperty(prop PropertyIdentifier, value interface{}) error
}

// NotificationSender 通知发送器接口
type NotificationSender interface {
	SendCOVNotification(clientAddr string, subscriptionID uint32, objectID uint32, propertyID uint32, newValue interface{}) error
}

// BACnetObject 实现基础的BACnet对象
type BACnetObject struct {
	Identifier            ObjectIdentifier                             // 对象标识符
	Name                  string                                       // 对象名称
	Properties            map[PropertyIdentifier]interface{}           // 属性值映射（优先级16，默认值）
	PrioritizedProperties map[PropertyIdentifier]map[uint8]interface{} // 优先级属性值映射（0-16，0最高）
	Events                []BACnetEvent                                // 事件列表
	Subscriptions         []COVSubscription                            // 变化通知订阅列表
	Notifier              NotificationSender                           // 通知发送器
}

// NewBACnetObject 创建一个新的BACnet对象
func NewBACnetObject(objType ObjectType, instance uint32, name string) *BACnetObject {
	return &BACnetObject{
		Identifier: ObjectIdentifier{
			Type:     objType,
			Instance: instance,
		},
		Name:                  name,
		Properties:            make(map[PropertyIdentifier]interface{}),
		PrioritizedProperties: make(map[PropertyIdentifier]map[uint8]interface{}),
		Events:                []BACnetEvent{},
		Subscriptions:         []COVSubscription{},
		Notifier:              nil, // 初始化为nil，由外部设置
	}
}

// GetObjectIdentifier 获取对象标识符
func (o *BACnetObject) GetObjectIdentifier() ObjectIdentifier {
	return o.Identifier
}

// GetObjectName 获取对象名称
func (o *BACnetObject) GetObjectName() string {
	return o.Name
}

// GetObjectType 获取对象类型
func (o *BACnetObject) GetObjectType() ObjectType {
	return o.Identifier.Type
}

// ReadProperty 读取对象属性
func (o *BACnetObject) ReadProperty(prop PropertyIdentifier) (interface{}, error) {
	// 按照BACnet协议，先检查高优先级值
	if o.PrioritizedProperties != nil {
		if priProps, exists := o.PrioritizedProperties[prop]; exists {
			// 从最高优先级(0)开始查找有效的值
			for priority := 0; priority < 16; priority++ {
				if value, ok := priProps[uint8(priority)]; ok && value != nil {
					return value, nil
				}
			}
		}
	}

	// 最后检查默认优先级(16)或直接存储的值
	if o.Properties != nil {
		value, exists := o.Properties[prop]
		if !exists {
			return nil, nil // 属性不存在
		}
		return value, nil
	}
	return nil, nil
}

// WriteProperty 写入对象属性（默认优先级16）
func (o *BACnetObject) WriteProperty(prop PropertyIdentifier, value interface{}) error {
	return o.WritePropertyWithPriority(prop, value, 16)
}

// WritePropertyWithPriority 按照BACnet协议，使用指定优先级写入对象属性
func (o *BACnetObject) WritePropertyWithPriority(prop PropertyIdentifier, value interface{}, priority uint8) error {
	// 初始化必要的映射
	if o.Properties == nil {
		o.Properties = make(map[PropertyIdentifier]interface{})
	}
	if o.PrioritizedProperties == nil {
		o.PrioritizedProperties = make(map[PropertyIdentifier]map[uint8]interface{})
	}

	// 获取当前有效值（用于比较是否变化）
	oldValue, _ := o.ReadProperty(prop)

	if priority == 16 {
		// 默认优先级，使用传统存储方式
		o.Properties[prop] = value
		// 清除其他优先级的对应值
		delete(o.PrioritizedProperties, prop)
	} else if priority >= 0 && priority <= 15 {
		// 优先级0-15，使用优先级存储
		if _, exists := o.PrioritizedProperties[prop]; !exists {
			o.PrioritizedProperties[prop] = make(map[uint8]interface{})
		}
		o.PrioritizedProperties[prop][priority] = value
	} else {
		return fmt.Errorf("invalid priority value, must be between 0-16")
	}

	// 获取新的有效值
	newValue, _ := o.ReadProperty(prop)

	// 如果有效值发生变化，则通知订阅者
	if oldValue != nil && newValue != nil && oldValue != newValue {
		o.NotifySubscribers(prop, oldValue, newValue)
	}
	return nil
}

// GetEventState 获取对象的事件状态
func (o *BACnetObject) GetEventState() EventState {
	if state, exists := o.Properties[PropertyIdentifierEventState]; exists {
		if s, ok := state.(EventState); ok {
			return s
		}
	}
	return EventStateNormal
}

// SetEventState 设置对象的事件状态
func (o *BACnetObject) SetEventState(state EventState) {
	o.Properties[PropertyIdentifierEventState] = state
}

// GetNotificationClass 获取通知类
func (o *BACnetObject) GetNotificationClass() uint32 {
	if class, exists := o.Properties[PropertyIdentifierNotificationClass]; exists {
		if c, ok := class.(uint32); ok {
			return c
		}
	}
	return 0
}

// SetNotificationClass 设置通知类
func (o *BACnetObject) SetNotificationClass(class uint32) {
	o.Properties[PropertyIdentifierNotificationClass] = class
}

// GetStatusFlags 获取状态标志
func (o *BACnetObject) GetStatusFlags() uint8 {
	if flags, exists := o.Properties[PropertyIdentifierStatusFlags]; exists {
		if f, ok := flags.(uint8); ok {
			return f
		}
	}
	return 0
}

// SetStatusFlags 设置状态标志
func (o *BACnetObject) SetStatusFlags(flags uint8) {
	o.Properties[PropertyIdentifierStatusFlags] = flags
}

// GenerateEvent 生成事件
func (o *BACnetObject) GenerateEvent(state EventState, message string) {
	event := BACnetEvent{
		EventType:         o.GetObjectType(),
		EventState:        state,
		TimeStamp:         time.Now(), // 使用当前时间
		MessageText:       message,
		NotificationClass: o.GetNotificationClass(),
	}
	o.Events = append(o.Events, event)
	o.SetEventState(state)

	// 更新状态标志
	flags := o.GetStatusFlags()
	if state != EventStateNormal {
		flags |= StatusFlagInAlarm
	} else {
		flags &^= StatusFlagInAlarm
	}
	o.SetStatusFlags(flags)
}

// AddCOVSubscription 添加一个COV订阅
func (o *BACnetObject) AddCOVSubscription(subscription COVSubscription) {
	o.Subscriptions = append(o.Subscriptions, subscription)
}

// RemoveCOVSubscription 移除指定ID的COV订阅
func (o *BACnetObject) RemoveCOVSubscription(subscriptionID uint32) bool {
	for i, sub := range o.Subscriptions {
		if sub.SubscriptionID == subscriptionID {
			o.Subscriptions = append(o.Subscriptions[:i], o.Subscriptions[i+1:]...)
			return true
		}
	}
	return false
}

// NotifySubscribers 通知所有订阅者属性变化
func (o *BACnetObject) NotifySubscribers(propertyIdentifier PropertyIdentifier, oldValue, newValue interface{}) {
	currentTime := time.Now() // 使用当前时间

	for i, sub := range o.Subscriptions {
		// 检查是否监控了该属性
		monitorThisProperty := false
		if len(sub.MonitoredProperties) == 0 {
			// 没有指定监控属性，则监控所有属性
			monitorThisProperty = true
		} else {
			for _, prop := range sub.MonitoredProperties {
				if prop == propertyIdentifier {
					monitorThisProperty = true
					break
				}
			}
		}

		if monitorThisProperty && sub.ClientAddress != "" {
			// 更新订阅时间戳
			o.Subscriptions[i].Timestamp = currentTime

			// 记录通知信息
			fmt.Printf("准备发送COV通知 - 订阅ID: %d, 对象: %s, 属性: %d, 新值: %v, 客户端: %s\n",
				sub.SubscriptionID, o.Name, propertyIdentifier, newValue, sub.ClientAddress)

			// 如果设置了Notifier，则使用它发送真实的COV通知
			if o.Notifier != nil {
				err := o.Notifier.SendCOVNotification(
					sub.ClientAddress,
					sub.SubscriptionID,
					uint32(o.Identifier.Instance),
					uint32(propertyIdentifier),
					newValue,
				)
				if err != nil {
					fmt.Printf("发送COV通知失败: %v\n", err)
				}
			} else {
				// 没有Notifier时，输出模拟发送日志
				fmt.Printf("[模拟] 向 %s 发送COV通知数据包\n", sub.ClientAddress)
			}

			// 处理确认COV通知
			if sub.IssueConfirmedCOVNotifications {
				fmt.Printf("[模拟] 向 %s 发送确认COV通知 - 订阅ID: %d\n", sub.ClientAddress, sub.SubscriptionID)
			}
		}
	}
}

// NewBACnetFile 创建一个新的BACnet文件对象
func NewBACnetFile(instance uint32, name string, accessMethod FileAccessMethod) *BACnetFile {
	fileObj := &BACnetFile{
		BACnetObject: NewBACnetObject(ObjectTypeFile, instance, name),
		FileData:     []byte{},
		AccessMethod: accessMethod,
		OpeningTag:   "",
		ClosingTag:   "",
	}

	// 设置文件对象的基本属性
	fileObj.WriteProperty(PropertyIdentifierFileSize, uint32(0))
	fileObj.WriteProperty(PropertyIdentifierFileAccessMethod, accessMethod)
	fileObj.WriteProperty(PropertyIdentifierFileOpeningTag, "")
	fileObj.WriteProperty(PropertyIdentifierFileClosingTag, "")

	return fileObj
}

// ReadFile 读取文件数据
func (f *BACnetFile) ReadFile(start uint32, count uint32) ([]byte, error) {
	if start >= uint32(len(f.FileData)) {
		return []byte{}, nil
	}

	end := start + count
	if end > uint32(len(f.FileData)) {
		end = uint32(len(f.FileData))
	}

	return f.FileData[start:end], nil
}

// WriteFile 写入文件数据
func (f *BACnetFile) WriteFile(start uint32, data []byte) error {
	if start > uint32(len(f.FileData)) {
		// 如果起始位置超出当前文件大小，先扩展文件
		newData := make([]byte, start+uint32(len(data)))
		copy(newData, f.FileData)
		f.FileData = newData
	} else if start+uint32(len(data)) > uint32(len(f.FileData)) {
		// 如果写入超出当前文件大小，扩展文件
		newData := make([]byte, start+uint32(len(data)))
		copy(newData, f.FileData[:start])
		f.FileData = newData
	}

	// 写入数据
	copy(f.FileData[start:], data)

	// 更新文件大小属性
	f.WriteProperty(PropertyIdentifierFileSize, uint32(len(f.FileData)))

	return nil
}

// DeleteFile 删除文件内容
func (f *BACnetFile) DeleteFile() error {
	f.FileData = []byte{}
	f.WriteProperty(PropertyIdentifierFileSize, uint32(0))
	return nil
}

// Device 表示BACnet设备对象
type Device struct {
	*BACnetObject
	Objects []Object
}

// NewDevice 创建一个新的BACnet设备
func NewDevice(instance uint32, name string, location string) *Device {
	device := &Device{
		BACnetObject: NewBACnetObject(ObjectTypeDevice, instance, name),
		Objects:      []Object{},
	}

	// 设置设备基本属性
	device.WriteProperty(PropertyIdentifierLocation, location)
	device.WriteProperty(PropertyIdentifierDeviceType, "Go BACnet Server")
	device.WriteProperty(PropertyIdentifierManufacturerName, "Go BACnet Simulator")
	device.WriteProperty(PropertyIdentifierModelName, "Simulator v1.0")
	device.WriteProperty(PropertyIdentifierFirmwareRevision, "1.0")
	device.WriteProperty(PropertyIdentifierApplicationSoftwareVersion, "1.0")

	return device
}

// AddObject 向设备添加对象
func (d *Device) AddObject(obj Object) {
	d.Objects = append(d.Objects, obj)
}

// FindObject 通过标识符查找对象
func (d *Device) FindObject(identifier ObjectIdentifier) Object {
	for _, obj := range d.Objects {
		if obj.GetObjectIdentifier() == identifier {
			return obj
		}
	}
	return nil
}
