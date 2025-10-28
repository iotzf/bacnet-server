package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/iotzf/bacnet-server/internal/model"
)

// BACnetServer 实现BACnet服务端
type BACnetServer struct {
	device            *model.Device
	udpConn           *net.UDPConn
	localAddr         *net.UDPAddr
	Running           bool
	currentClientAddr string // 当前客户端地址，用于COV订阅
}

// NewBACnetServer 创建一个新的BACnet服务端
func NewBACnetServer(device *model.Device, host string) (*BACnetServer, error) {
	// 创建UDP连接
	addr, err := net.ResolveUDPAddr("udp", host) // BACnet默认端口
	if err != nil {
		return nil, err
	}

	udpConn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}

	return &BACnetServer{
		device:    device,
		udpConn:   udpConn,
		localAddr: addr,
		Running:   false,
	}, nil
}

// Start 启动BACnet服务端
func (s *BACnetServer) Start() {
	s.Running = true
	fmt.Printf("BACnet Server started on port %d\n", s.localAddr.Port)
	fmt.Printf("Device ID: %d, Name: %s\n", s.device.GetObjectIdentifier().Instance, s.device.GetObjectName())

	go s.handleRequests()
}

// Stop 停止BACnet服务端
func (s *BACnetServer) Stop() {
	s.Running = false
	if s.udpConn != nil {
		s.udpConn.Close()
	}
	fmt.Println("BACnet Server stopped")
}

// 添加对象到BACnet服务器
func (s *BACnetServer) AddObject(obj model.Object) {
	s.device.AddObject(obj)
}

// SimulateDataChange 模拟设备数据变化并触发COV通知
// 此方法仅用于演示目的，可以手动调用以测试COV通知功能
func (s *BACnetServer) SimulateDataChange(objectInstance uint32, property model.PropertyIdentifier, newValue interface{}) {
	// 由于没有直接通过实例ID查找对象的方法，这里遍历查找
	var targetObject model.Object

	for _, obj := range s.device.Objects {
		if obj.GetObjectIdentifier().Instance == objectInstance {
			targetObject = obj
			break
		}
	}

	if targetObject == nil {
		fmt.Printf("未找到实例ID为%d的对象\n", objectInstance)
		return
	}

	// 获取当前值
	oldValue, _ := targetObject.ReadProperty(property)

	// 更新属性值（会自动触发NotifySubscribers）
	targetObject.WriteProperty(property, newValue)

	fmt.Printf("模拟数据变化: 对象实例=%d, 属性=%d, 旧值=%v, 新值=%v\n",
		objectInstance, property, oldValue, newValue)
}

// SendCOVNotification 发送COV通知给指定客户端
func (s *BACnetServer) SendCOVNotification(clientAddr string, subscriptionID uint32, objectID uint32, propertyID uint32, newValue interface{}) error {
	if s.udpConn == nil {
		return fmt.Errorf("UDP连接未初始化")
	}

	// 解析客户端地址
	addr, err := net.ResolveUDPAddr("udp", clientAddr)
	if err != nil {
		return fmt.Errorf("无效的客户端地址: %v", err)
	}

	// 编码属性值
	propertyValueBytes := encodePropertyValue(propertyID, newValue)

	// 计算消息体长度（不包括BVLC头部）
	npduLength := 10                                          // NPDU固定长度
	apduLength := 3 + 4 + 4 + 4 + 1 + len(propertyValueBytes) // APDU长度 = 头部(3) + 订阅ID(4) + 设备ID(4) + 对象ID(4) + 属性列表计数(1) + 属性值列表
	messageBodyLength := npduLength + apduLength

	// 计算总长度（包括BVLC头部）
	totalLength := 4 + messageBodyLength // BVLC头部长度为4字节

	// 创建完整的COV通知消息
	notification := []byte{
		0x81,                                             // BVLC类型: BACnet/IP
		0x00,                                             // BVLC函数: 原始UDP
		byte(totalLength >> 8), byte(totalLength & 0xFF), // 总长度
		0x00, 0x00, 0x00, 0x00, // BVLC数据
		0x01,       // NPDU版本
		0x00,       // NPDU控制
		0x00,       // NPDU目标网络
		0x00, 0x00, // NPDU目标MAC地址
		0x00,       // NPDU源网络
		0x00, 0x00, // NPDU源MAC地址
		0x00,             // NPDU跳数
		0x05,             // APDU类型: 未确认服务请求
		0x00,             // 服务选择
		byte(apduLength), // 服务数据长度
		0x0A,             // 服务类型: COV通知
		// 订阅ID
		byte(subscriptionID >> 24), byte(subscriptionID >> 16), byte(subscriptionID >> 8), byte(subscriptionID),
		// 通知设备ID (使用服务器设备ID)
		byte(s.device.GetObjectIdentifier().Instance >> 24),
		byte(s.device.GetObjectIdentifier().Instance >> 16),
		byte(s.device.GetObjectIdentifier().Instance >> 8),
		byte(s.device.GetObjectIdentifier().Instance),
		// 监控对象ID
		byte(objectID >> 24), byte(objectID >> 16), byte(objectID >> 8), byte(objectID),
		0x01, // 属性列表计数（1个属性）
	}

	// 添加编码后的属性值
	notification = append(notification, propertyValueBytes...)

	// 发送通知
	n, err := s.udpConn.WriteToUDP(notification, addr)
	if err != nil {
		return fmt.Errorf("发送COV通知失败: %v", err)
	}

	fmt.Printf("已发送COV通知至 %s, 订阅ID: %d, 属性ID: %d, 新值: %v, 字节数: %d\n",
		clientAddr, subscriptionID, propertyID, newValue, n)
	return nil
}

// encodePropertyValue 根据BACnet协议编码属性值
func encodePropertyValue(propertyID uint32, value interface{}) []byte {
	var result []byte

	// 添加属性ID
	result = append(result, byte(propertyID>>8), byte(propertyID&0xFF))

	// 跳过优先级字段（使用默认优先级）
	result = append(result, 0xFF)

	// 根据值类型进行编码
	switch v := value.(type) {
	case bool:
		// 布尔类型: 类型标识 0x11
		result = append(result, 0x11)
		if v {
			result = append(result, 0x01)
		} else {
			result = append(result, 0x00)
		}
	case int, int32, int64:
		// 有符号整数类型: 类型标识 0x25
		result = append(result, 0x25)
		// 使用类型断言并转换为int32
		var intValue int32
		switch val := v.(type) {
		case int:
			intValue = int32(val)
		case int32:
			intValue = val
		case int64:
			intValue = int32(val)
		}
		result = append(result,
			byte(intValue>>24),
			byte(intValue>>16),
			byte(intValue>>8),
			byte(intValue&0xFF),
		)
	case uint, uint32, uint64:
		// 无符号整数类型: 类型标识 0x27
		result = append(result, 0x27)
		// 使用类型断言并转换为uint32
		var uintValue uint32
		switch val := v.(type) {
		case uint:
			uintValue = uint32(val)
		case uint32:
			uintValue = val
		case uint64:
			uintValue = uint32(val)
		}
		result = append(result,
			byte(uintValue>>24),
			byte(uintValue>>16),
			byte(uintValue>>8),
			byte(uintValue&0xFF),
		)
	case float32:
		// 浮点数类型: 类型标识 0x29 (单精度)
		result = append(result, 0x29)
		// 将float32转换为4字节表示
		bits := math.Float32bits(v)
		result = append(result,
			byte(bits>>24),
			byte(bits>>16),
			byte(bits>>8),
			byte(bits&0xFF),
		)
	case float64:
		// 浮点数类型: 类型标识 0x2A (双精度)
		result = append(result, 0x2A)
		// 将float64转换为8字节表示
		bits := math.Float64bits(v)
		result = append(result,
			byte(bits>>56),
			byte(bits>>48),
			byte(bits>>40),
			byte(bits>>32),
			byte(bits>>24),
			byte(bits>>16),
			byte(bits>>8),
			byte(bits&0xFF),
		)
	case string:
		// 字符串类型: 类型标识 0x30
		result = append(result, 0x30)
		// 添加字符串长度
		if len(v) < 255 {
			result = append(result, byte(len(v)))
		} else {
			// 最大支持254字节长度的字符串
			result = append(result, 0xFE)
			v = v[:254]
		}
		// 添加字符串内容
		result = append(result, []byte(v)...)
	case time.Time:
		// 时间戳类型: 类型标识 0xC4 (BACnetDateTime)
		result = append(result, 0xC4)
		// 按照BACnet协议规范完整实现DateTime编码
		year := uint16(v.Year())

		// 计算星期几 (BACnet中0=未指定, 1=周一, 2=周二, ..., 7=周日)
		weekday := v.Weekday()
		weekdayCode := byte(0) // 默认未指定
		if weekday >= time.Monday && weekday <= time.Sunday {
			weekdayCode = byte(weekday) + 1 // 转换为BACnet格式
		}

		// 计算夏令时状态 (0=未知, 1=标准时间, 2=夏令时) - BACnet协议实现
		dstCode := byte(1) // 默认标准时间

		// 尝试检测夏令时状态 - 按照BACnet协议实现
		// 1. 首先通过时区名称检测常见的夏令时标识
		zoneName, offset := v.Zone()

		// 2. BACnet协议夏令时检测方法：
		// - 检查时区名称是否包含夏令时标识
		// - 比较当前时间与同一时间点在UTC时区的时间偏移量
		if len(zoneName) > 0 {
			// 检测常见夏令时时区名称
			if strings.Contains(strings.ToUpper(zoneName), "DST") ||
				strings.Contains(zoneName, "夏") ||
				strings.Contains(strings.ToUpper(zoneName), "SUMMER") ||
				strings.Contains(strings.ToUpper(zoneName), "DAYLIGHT") {
				dstCode = byte(2) // 夏令时
			} else {
				// 3. 更精确的检测：比较UTC偏移量与时区标准偏移量
				// 创建UTC时间并转换回本地时区以获取标准偏移量
				utcTime := v.UTC()
				_, stdOffset := utcTime.In(time.Local).Zone()

				// 如果当前偏移量与标准偏移量不同，可能处于夏令时
				// 注意：某些时区标准偏移量就是非零的，所以需要谨慎判断
				// BACnet协议建议：当时间偏移量增加1小时且不是UTC+1时，判定为夏令时
				if offset != stdOffset && (offset-stdOffset) == 3600 { // 1小时偏移
					dstCode = byte(2) // 夏令时
				}
			}
		}

		// 计算小数秒 (使用纳秒部分)
		fractionalSeconds := byte(float64(v.Nanosecond()) / 10000000.0) // 0-99范围

		// 添加完整的BACnetDateTime字段
		result = append(result,
			byte(year>>8), byte(year&0xFF), // 年 (2字节)
			byte(v.Month()),   // 月 (1字节, 1-12)
			byte(v.Day()),     // 日 (1字节, 1-31)
			byte(v.Hour()),    // 时 (1字节, 0-23)
			byte(v.Minute()),  // 分 (1字节, 0-59)
			byte(v.Second()),  // 秒 (1字节, 0-59)
			fractionalSeconds, // 小数秒 (1字节, 0-99)
			weekdayCode,       // 星期几 (1字节, 0=未指定, 1-7)
			dstCode,           // 夏令时状态 (1字节, 0=未知, 1=标准, 2=夏令时)
		)
	default:
		// 未知类型，使用默认值
		result = append(result, 0x27, 0x00, 0x00, 0x00, 0x00)
	}

	return result
}

// handleRequests 处理接收到的BACnet请求
func (s *BACnetServer) handleRequests() {
	buffer := make([]byte, 4096)

	for s.Running {
		n, addr, err := s.udpConn.ReadFromUDP(buffer)
		if err != nil {
			if s.Running { // 只在运行状态下报告错误
				fmt.Printf("Error reading from UDP: %v\n", err)
			}
			continue
		}

		if n > 0 {
			// 处理接收到的数据包
			data := buffer[:n]
			fmt.Printf("Received %d bytes from %s\n", n, addr.String())

			// 保存客户端地址，用于COV订阅
			s.currentClientAddr = addr.String()

			// 解析并处理BACnet消息
			response, err := s.processBACnetMessage(data)
			if err != nil {
				fmt.Printf("Error processing BACnet message: %v\n", err)
				continue
			}

			// 如果有响应需要发送
			if len(response) > 0 {
				_, err = s.udpConn.WriteToUDP(response, addr)
				if err != nil {
					fmt.Printf("Error sending response: %v\n", err)
				}
			}
		}
	}
}

// processBACnetMessage 处理BACnet消息并返回响应
func (s *BACnetServer) processBACnetMessage(data []byte) ([]byte, error) {
	// 检查最小长度
	if len(data) < 4 {
		return nil, fmt.Errorf("BACnet message too short")
	}

	bvlc := data[0]
	bvlcFunction := data[1]
	bvlcLength := binary.BigEndian.Uint16(data[2:4])

	// 检查BVLC类型 (应该是0x81表示BACnet/IP)
	if bvlc != 0x81 {
		return nil, fmt.Errorf("unknown BVLC type: %02x", bvlc)
	}
	if int(bvlcLength) != len(data) {
		return nil, fmt.Errorf("BVLC length mismatch: expected %d, got %d", bvlcLength, len(data))
	}

	// 处理不同类型的BVLC函数
	switch bvlcFunction {
	case 0x0a: // 原始UDP消息 Original-Unicast-NPDU
		return s.handleOriginalUDPMessage(data[4:])
	case 0x0b: // 广播消息 Original-Broadcast-NPDU 用于向网络中的所有BACnet设备发送消息（如Who-Is请求）
		return s.handleBroadcastMessage(data[4:])
	default:
		fmt.Printf("Unsupported BVLC function: %02x\n", data[1])
		return nil, nil
	}
}

// handleOriginalUDPMessage 处理原始UDP消息
func (s *BACnetServer) handleOriginalUDPMessage(data []byte) ([]byte, error) {
	_, offset, err := ParseNPDU(data)
	if err != nil {
		return nil, err
	}
	return s.handleBACnetAPDU(data[offset:])
}

// handleBroadcastMessage 处理广播消息
func (s *BACnetServer) handleBroadcastMessage(data []byte) ([]byte, error) {
	_, offset, err := ParseNPDU(data)
	if err != nil {
		return nil, err
	}
	return s.handleBACnetAPDU(data[offset:])
}

// 错误类型常量
const (
	ErrorClassDevice                  = 0x01
	ErrorClassObject                  = 0x02
	ErrorClassProperty                = 0x03
	ErrorClassService                 = 0x04
	ErrorClassCov                     = 0x09 // COV错误类
	ErrorCodeObjectNotExist           = 0x01
	ErrorCodePropertyNotExist         = 0x02
	ErrorCodePropertyNotReadable      = 0x03
	ErrorCodePropertyNotWritable      = 0x04
	ErrorCodeValueOutOfRange          = 0x05
	ErrorCodeInvalidParameterDataType = 0x07 // 参数数据类型无效
	ErrorCodeObjectNotOfRequiredType  = 0x06 // 对象类型不正确
	ErrorCodeInvalidDataType          = 0x02 // 无效的数据类型 (与ErrorCodePropertyNotExist相同值，但用于不同场景)
	ErrorCodeCovObject                = 0x01 // COV对象错误
	ErrorCodeCovProperty              = 0x02 // COV属性错误
	ErrorCodeCovInvalidTime           = 0x03 // COV无效时间
)

// 文件操作错误常量
const (
	ErrorClassFile             = 0x06 // 文件操作错误类
	ErrorCodeFileAccessDenied  = 0x01 // 文件访问被拒绝
	ErrorCodeFileNotFound      = 0x02 // 文件未找到
	ErrorCodeFileAlreadyExists = 0x03 // 文件已存在
	ErrorCodeFileTooLarge      = 0x04 // 文件太大
	ErrorCodeFileDirectory     = 0x05 // 文件目录
	ErrorCodeFileNotDirectory  = 0x06 // 不是文件目录
	ErrorCodeFileReadFault     = 0x07 // 文件读取错误
	ErrorCodeFileWriteFault    = 0x08 // 文件写入错误
)

// handleBACnetAPDU 处理BACnet APDU消息
func (s *BACnetServer) handleBACnetAPDU(data []byte) ([]byte, error) {
	// 检查数据长度
	if len(data) < 2 {
		return nil, fmt.Errorf("APDU too short")
	}

	// 获取APDU类型
	apdu, err := ParseAPDU(data)
	if err != nil {
		return nil, err
	}
	fmt.Printf("apdu type: %s\n", apdu.String())

	// 根据APDU类型处理请求
	switch apdu.PDUType {
	case BACnetAPDUTypeConfirmedServiceRequest:
		// Confirmed service request 需要 invokeID 和 serviceChoice
		if apdu.InvokeID == nil || apdu.ServiceChoice == nil {
			return nil, fmt.Errorf("confirmed service request missing invokeID or serviceChoice")
		}

		invokeID := *apdu.InvokeID
		switch *apdu.ServiceChoice {
		case BACnetServiceConfirmedReadProperty:
			fmt.Println("Received ReadProperty request")
			return s.handleReadProperty(apdu.Payload, invokeID)
		case BACnetServiceConfirmedWriteProperty:
			fmt.Println("Received WriteProperty request")
			return s.handleWriteProperty(apdu.Payload, invokeID)
		case BACnetServiceConfirmedReadPropertyMultiple:
			fmt.Println("Received ReadPropertyMultiple request")
			return s.handleReadPropertyMultiple(apdu.Payload, invokeID)
		case BACnetServiceConfirmedWritePropertyMultiple:
			fmt.Println("Received WritePropertyMultiple request")
			return s.handleWritePropertyMultiple(apdu.Payload, invokeID)
		case BACnetServiceConfirmedAcknowledgeAlarm:
			fmt.Println("Received AcknowledgeAlarm request")
			return s.handleAcknowledgeAlarm(apdu.Payload, invokeID)
		case BACnetServiceConfirmedAtomicReadFile:
			fmt.Println("Received AtomicReadFile request")
			return s.handleAtomicReadFile(apdu.Payload, invokeID)
		case BACnetServiceConfirmedAtomicWriteFile:
			fmt.Println("Received AtomicWriteFile request")
			return s.handleAtomicWriteFile(apdu.Payload, invokeID)
		case BACnetServiceConfirmedDeleteFile:
			fmt.Println("Received DeleteFile request")
			return s.handleDeleteFile(apdu.Payload, invokeID)
		case BACnetServiceConfirmedSubscribeCOV:
			fmt.Println("Received SubscribeCOV request")
			return s.handleSubscribeCOV(apdu.Payload, invokeID)
		case BACnetServiceConfirmedSubscribeCOVProperty:
			fmt.Println("Received SubscribeCOVProperty request")
			return s.handleSubscribeCOVProperty(apdu.Payload, invokeID)
		case BACnetServiceConfirmedCancelCOVSubscription:
			fmt.Println("Received CancelCOVSubscription request")
			return s.handleCancelCOVSubscription(apdu.Payload, invokeID)
		default:
			fmt.Printf("Unsupported service type: %02x\n", *apdu.ServiceChoice)
		}
	case BACnetAPDUTypeUnconfirmedServiceRequest:
		// Unconfirmed service request 可能没有 invokeID
		if apdu.ServiceChoice == nil {
			fmt.Println("Unconfirmed service without serviceChoice")
			return nil, fmt.Errorf("unconfirmed service request missing serviceChoice")
		}

		switch *apdu.ServiceChoice {
		case BACnetServiceUnconfirmedWhoIs:
			fmt.Println("Received Who-Is request")
			return s.createIAmResponse(), nil
		default:
			return nil, fmt.Errorf("Unsupported unconfirmed service type: 0x%02x\n", *apdu.ServiceChoice)
		}
	case BACnetAPDUTypeSimpleAck:
		// 按照BACnet协议规范处理SimpleAck
		// SimpleAck用于确认接收到确认服务请求并成功处理
		invokeID := "未知"
		serviceName := "未知"

		if apdu.InvokeID != nil {
			invokeID = fmt.Sprintf("0x%02x", *apdu.InvokeID)
		}

		if apdu.ServiceChoice != nil {
			serviceName = apdu.ServiceName()
		}

		// 记录SimpleAck信息，符合BACnet协议规范的处理
		fmt.Printf("收到SimpleAck: 服务=%s, InvokeID=%s\n", serviceName, invokeID)

		// 根据BACnet协议，服务器接收到SimpleAck通常不需要回复
		return nil, nil
	case BACnetAPDUTypeComplexAck:
		// 按照BACnet协议规范处理ComplexAck APDU
		// ComplexAck用于确认服务请求并提供复杂的响应数据
		invokeID := "未知"
		serviceName := "未知"
		segmented := "否"
		moreFollows := "否"
		sequenceNumber := 0
		proposedWindowSize := 0
		payloadSize := len(apdu.Payload)

		// 获取InvokeID（请求标识）
		if apdu.InvokeID != nil {
			invokeID = fmt.Sprintf("0x%02x", *apdu.InvokeID)
		}

		// 获取控制标志信息
		// 解析分段控制标志
		if (apdu.ControlFlags)&0x01 == 0x01 {
			segmented = "是"
		}
		if (apdu.ControlFlags)&0x02 == 0x02 {
			moreFollows = "是"
		}

		// 获取服务名称
		if apdu.ServiceChoice != nil {
			serviceName = apdu.ServiceName()
		}

		// 解析分段信息（如果适用）
		if segmented == "是" && len(apdu.Payload) >= 2 {
			sequenceNumber = int(apdu.Payload[0])
			proposedWindowSize = int(apdu.Payload[1])
			// 记录分段信息
			fmt.Printf("收到ComplexAck APDU: 服务=%s, InvokeID=%s, 分段=%s, 更多跟随=%s, 序列号=%d, 提议窗口大小=%d, 有效载荷大小=%d字节\n",
				serviceName, invokeID, segmented, moreFollows, sequenceNumber, proposedWindowSize, payloadSize)
		} else {
			// 非分段ComplexAck
			fmt.Printf("收到ComplexAck APDU: 服务=%s, InvokeID=%s, 分段=%s, 有效载荷大小=%d字节\n",
				serviceName, invokeID, segmented, payloadSize)
		}

		// 根据BACnet协议，服务器收到ComplexAck通常不需要回复
		return nil, nil
	case BACnetAPDUTypeSegmentAck:
		// 按照BACnet协议规范处理SegmentAck APDU
		// SegmentAck用于在分段传输场景中确认收到的数据段
		invokeID := "未知"
		sequenceNumber := 0
		proposedWindowSize := 0
		neglectStart := "否"
		fragmented := "否"
		serverInitiated := "否"

		// 获取InvokeID（请求标识）
		if apdu.InvokeID != nil {
			invokeID = fmt.Sprintf("0x%02x", *apdu.InvokeID)
		}

		// 解析控制标志
		// 第一位表示Neglect Start
		if (apdu.ControlFlags)&0x08 == 0x08 {
			neglectStart = "是"
		}
		// 第二位表示Fragmented
		if (apdu.ControlFlags)&0x04 == 0x04 {
			fragmented = "是"
		}
		// 第四位表示Server Initiated
		if (apdu.ControlFlags)&0x01 == 0x01 {
			serverInitiated = "是"
		}

		// 解析序列号和提议窗口大小（BACnet协议规定在payload中）
		if len(apdu.Payload) >= 2 {
			sequenceNumber = int(apdu.Payload[0])
			proposedWindowSize = int(apdu.Payload[1])
		}

		// 记录SegmentAck信息，符合BACnet协议规范的处理
		fmt.Printf("收到SegmentAck APDU: InvokeID=%s, 序列号=%d, 提议窗口大小=%d, 忽略开始=%s, 分段=%s, 服务器发起=%s\n",
			invokeID, sequenceNumber, proposedWindowSize, neglectStart, fragmented, serverInitiated)

		// 根据BACnet协议，服务器收到SegmentAck后通常不需要回复
		return nil, nil
	case BACnetAPDUTypeError:
		// 按照BACnet协议规范处理Error APDU
		// Error用于指示服务请求已被拒绝，并提供错误详情
		errorClass := "未知"
		errorCode := "未知"
		invokeID := "未知"
		serviceName := "未知"
		classCode := uint8(0)
		code := uint8(0)

		// 获取InvokeID（如果存在）
		if apdu.InvokeID != nil {
			invokeID = fmt.Sprintf("0x%02x", *apdu.InvokeID)
		}

		// 获取服务名称（如果存在）
		if apdu.ServiceChoice != nil {
			serviceName = apdu.ServiceName()
		}

		// 解析错误类别和错误代码（BACnet协议规定在payload中）
		if len(apdu.Payload) >= 2 {
			classCode = apdu.Payload[0]
			code = apdu.Payload[1]

			// 解析错误类别
			switch classCode {
			case ErrorClassDevice:
				errorClass = "设备错误"
				// 设备错误子类解析
				switch code {
				case 0:
					errorCode = "设备忙"
				case 1:
					errorCode = "无内存"
				case 2:
					errorCode = "资源不可用"
				default:
					errorCode = fmt.Sprintf("未知设备错误(0x%02x)", code)
				}
			case ErrorClassObject:
				errorClass = "对象错误"
				// 对象错误子类解析
				switch code {
				case ErrorCodeObjectNotExist:
					errorCode = "对象不存在"
				case ErrorCodeObjectNotOfRequiredType:
					errorCode = "对象类型不正确"
				default:
					errorCode = fmt.Sprintf("未知对象错误(0x%02x)", code)
				}
			case ErrorClassProperty:
				errorClass = "属性错误"
				// 属性错误子类解析
				switch code {
				case ErrorCodePropertyNotExist:
					errorCode = "属性不存在"
				case ErrorCodePropertyNotReadable:
					errorCode = "属性不可读"
				case ErrorCodePropertyNotWritable:
					errorCode = "属性不可写"
				case ErrorCodeValueOutOfRange:
					errorCode = "值超出范围"
				default:
					errorCode = fmt.Sprintf("未知属性错误(0x%02x)", code)
				}
			case ErrorClassService:
				errorClass = "服务错误"
				// 服务错误子类解析
				switch code {
				case 0:
					errorCode = "服务请求被拒绝"
				case 1:
					errorCode = "服务未实现"
				case 2:
					errorCode = "服务不可用"
				case ErrorCodeInvalidParameterDataType:
					errorCode = "参数数据类型无效"
				default:
					errorCode = fmt.Sprintf("未知服务错误(0x%02x)", code)
				}
			case ErrorClassCov:
				errorClass = "COV错误"
				// COV错误子类解析
				switch code {
				case ErrorCodeCovObject:
					errorCode = "COV对象错误"
				case ErrorCodeCovProperty:
					errorCode = "COV属性错误"
				case ErrorCodeCovInvalidTime:
					errorCode = "COV无效时间"
				default:
					errorCode = fmt.Sprintf("未知COV错误(0x%02x)", code)
				}
			case ErrorClassFile:
				errorClass = "文件错误"
				// 文件错误子类解析
				switch code {
				case ErrorCodeFileAccessDenied:
					errorCode = "文件访问被拒绝"
				case ErrorCodeFileNotFound:
					errorCode = "文件未找到"
				case ErrorCodeFileAlreadyExists:
					errorCode = "文件已存在"
				case ErrorCodeFileTooLarge:
					errorCode = "文件太大"
				case ErrorCodeFileDirectory:
					errorCode = "文件目录"
				case ErrorCodeFileNotDirectory:
					errorCode = "不是文件目录"
				case ErrorCodeFileReadFault:
					errorCode = "文件读取错误"
				case ErrorCodeFileWriteFault:
					errorCode = "文件写入错误"
				default:
					errorCode = fmt.Sprintf("未知文件错误(0x%02x)", code)
				}
			default:
				errorClass = fmt.Sprintf("未知错误类别(0x%02x)", classCode)
				errorCode = fmt.Sprintf("未知错误代码(0x%02x)", code)
			}
		}

		// 记录Error信息，符合BACnet协议规范的处理
		fmt.Printf("收到Error APDU: 服务=%s, InvokeID=%s, 错误类别=0x%02x(%s), 错误代码=0x%02x(%s)\n",
			serviceName, invokeID, classCode, errorClass, code, errorCode)

		// 根据BACnet协议，服务器接收到Error通常不需要回复
		return nil, nil
	case BACnetAPDUTypeReject:
		// 按照BACnet协议规范处理Reject APDU
		// Reject用于指示请求因格式或语义错误而被拒绝
		rejectReason := "未知"
		reasonCode := uint8(0)
		invokeID := "未知"

		// 获取InvokeID（如果存在）
		if apdu.InvokeID != nil {
			invokeID = fmt.Sprintf("0x%02x", *apdu.InvokeID)
		}

		// 解析拒绝原因代码（BACnet协议规定在InvokeID后面的字节）
		if len(apdu.Payload) > 0 {
			reasonCode = apdu.Payload[0]
			// 根据BACnet协议定义的拒绝原因代码解释
			switch reasonCode {
			case 0:
				rejectReason = "其他原因"
			case 1:
				rejectReason = "缓冲区溢出"
			case 2:
				rejectReason = "无效应用标签"
			case 3:
				rejectReason = "无效标签类型"
			case 4:
				rejectReason = "标签长度值无效"
			case 5:
				rejectReason = "请求的缓冲区太大"
			case 6:
				rejectReason = "分段消息不完整"
			case 7:
				rejectReason = "服务请求参数数量无效"
			case 8:
				rejectReason = "服务请求参数类型无效"
			case 9:
				rejectReason = "服务请求参数值无效"
			case 10:
				rejectReason = "确认服务请求服务选择器无效"
			case 11:
				rejectReason = "确认服务请求缓冲区太大"
			default:
				rejectReason = fmt.Sprintf("未知拒绝原因(0x%02x)", reasonCode)
			}
		}

		// 记录Reject信息，符合BACnet协议规范的处理
		fmt.Printf("收到Reject APDU: InvokeID=%s, 拒绝原因代码=0x%02x, 原因=%s\n",
			invokeID, reasonCode, rejectReason)

		// 根据BACnet协议，服务器接收到Reject通常不需要回复
		return nil, nil
	case BACnetAPDUTypeAbort:
		// 按照BACnet协议规范处理Abort APDU
		// Abort用于指示通信会话被意外终止
		abortReason := "未知"
		reasonCode := uint8(0)
		invokeID := "未知"
		isServer := false

		// 获取InvokeID（如果存在）
		if apdu.InvokeID != nil {
			invokeID = fmt.Sprintf("0x%02x", *apdu.InvokeID)
		}

		// 解析控制标志中的服务器发起标志
		if (apdu.ControlFlags & 0x01) != 0 {
			isServer = true
		}

		// 解析放弃原因代码（BACnet协议规定在适当位置）
		if len(apdu.Payload) > 0 {
			reasonCode = apdu.Payload[0]
			// 根据BACnet协议定义的放弃原因代码解释
			switch reasonCode {
			case 0:
				abortReason = "其他原因"
			case 1:
				abortReason = "缓冲区溢出"
			case 2:
				abortReason = "非预期的PDU类型"
			case 3:
				abortReason = "非预期的InvokeID"
			case 4:
				abortReason = "未确认的服务请求"
			case 5:
				abortReason = "超时"
			case 6:
				abortReason = "处理错误"
			case 7:
				abortReason = "服务器已停止"
			case 8:
				abortReason = "无效的参数数据类型"
			case 9:
				abortReason = "无效的参数值"
			case 10:
				abortReason = "设备忙"
			case 11:
				abortReason = "服务器资源不足"
			default:
				abortReason = fmt.Sprintf("未知放弃原因(0x%02x)", reasonCode)
			}
		}

		// 记录Abort信息，符合BACnet协议规范的处理
		fmt.Printf("收到Abort APDU: InvokeID=%s, 发起方=%s, 放弃原因代码=0x%02x, 原因=%s\n",
			invokeID,
			func() string {
				if isServer {
					return "服务器"
				} else {
					return "客户端"
				}
			}(),
			reasonCode,
			abortReason)

		// 根据BACnet协议，服务器接收到Abort通常不需要回复
		return nil, nil
	default:
		return nil, fmt.Errorf("Unhandled APDU: % x\n", data)
	}

	return nil, nil
}

// parseObjectIdentifier 解析对象标识符
func parseObjectIdentifier(data []byte) (model.ObjectIdentifier, int, error) {
	if len(data) < 4 {
		return model.ObjectIdentifier{}, 0, fmt.Errorf("数据太短，无法解析对象标识符")
	}

	// 解析对象类型和实例
	typeAndInstance := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	objectType := model.ObjectType(typeAndInstance >> 22)
	instance := typeAndInstance & 0x3FFFFF

	return model.ObjectIdentifier{
		Type:     objectType,
		Instance: instance,
	}, 4, nil
}

// parsePropertyIdentifier 解析属性标识符
// BACnet协议中，属性标识符使用2字节大端序格式编码
func parsePropertyIdentifier(data []byte) (model.PropertyIdentifier, int, error) {
	if len(data) < 2 {
		return 0, 0, fmt.Errorf("数据太短，无法解析属性标识符")
	}

	// 按照BACnet协议规范，使用2字节大端序格式解析属性标识符
	// 高字节(data[0])包含属性标识符的高位，低字节(data[1])包含属性标识符的低位
	propID := model.PropertyIdentifier(uint32(data[0])<<8 | uint32(data[1]))

	// 返回解析后的属性标识符、消耗的字节数和nil错误
	return propID, 2, nil
}

// encodeObjectIdentifier 编码对象标识符为BACnet格式
func encodeObjectIdentifier(oid model.ObjectIdentifier) []byte {
	// BACnet格式：类型占10位，实例占22位
	typeAndInstance := uint32(oid.Type)<<22 | (oid.Instance & 0x3FFFFF)
	return []byte{
		byte(typeAndInstance >> 24),
		byte(typeAndInstance >> 16),
		byte(typeAndInstance >> 8),
		byte(typeAndInstance),
	}
}

// encodePropertyIdentifier 编码属性标识符为BACnet格式
func encodePropertyIdentifier(propID model.PropertyIdentifier) []byte {
	// BACnet协议中，属性标识符使用2字节大端序格式编码
	// 确保属性标识符在2字节范围内
	if uint32(propID) > 0xFFFF {
		// 如果超出范围，返回一个默认值或错误处理
		// 这里我们使用大端序编码，但限制在2字节内
		return []byte{
			byte(0xFF),
			byte(0xFF),
		}
	}

	// 正确的大端序编码实现
	return []byte{
		byte(uint32(propID) >> 8), // 高字节
		byte(propID & 0xFF),       // 低字节
	}
}

// createErrorResponse 创建错误响应
func (s *BACnetServer) createErrorResponse(invokeID byte, serviceType byte, errorClass, errorCode byte) []byte {
	response := []byte{
		BACnetAPDUTypeError | 0x01, // APDU类型：错误，服务确认
		0x00,                       // Reserved
		invokeID,                   // 与请求相同的invokeID
		0x03,                       // 错误长度
		serviceType,                // 原始服务类型
		errorClass,                 // 错误类别
		errorCode,                  // 错误代码
	}
	return response
}

// encodeBACnetValue 编码BACnet值为字节数组
func encodeBACnetValue(value interface{}) []byte {
	var result []byte

	switch v := value.(type) {
	case bool:
		result = append(result, 0x11) // BOOLEAN类型
		if v {
			result = append(result, 0x01)
		} else {
			result = append(result, 0x00)
		}
	case uint8:
		result = append(result, 0x21) // UNSIGNED INTEGER 8
		result = append(result, v)
	case uint16:
		result = append(result, 0x22) // UNSIGNED INTEGER 16
		result = append(result, byte(v>>8), byte(v))
	case uint32:
		result = append(result, 0x23) // UNSIGNED INTEGER 32
		result = append(result, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	case float32:
		result = append(result, 0x39) // REAL类型
		// 转换为IEEE 754格式
		uintBits := math.Float32bits(v)
		result = append(result, byte(uintBits>>24), byte(uintBits>>16), byte(uintBits>>8), byte(uintBits))
	case string:
		result = append(result, 0x41) // CHARACTER STRING类型
		result = append(result, byte(len(v)))
		result = append(result, []byte(v)...)
	default:
		// 未知类型，返回空值
		result = append(result, 0x00) // NULL类型
	}

	return result
}

// handleReadProperty 处理读取属性请求
func (s *BACnetServer) handleReadProperty(data []byte, invokeID byte) ([]byte, error) {
	// 解析对象标识符
	objectID, offset, err := parseObjectIdentifier(data)
	if err != nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedReadProperty, ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 解析属性标识符
	propertyID, _, err := parsePropertyIdentifier(data[offset:])
	if err != nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedReadProperty, ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 查找对象
	var targetObj model.Object

	// 检查是否是设备对象本身
	if objectID.Type == model.ObjectTypeDevice && objectID.Instance == s.device.GetObjectIdentifier().Instance {
		targetObj = s.device
	} else {
		// 在设备的对象列表中查找
		targetObj = s.device.FindObject(objectID)
	}

	// 对象不存在
	if targetObj == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedReadProperty, ErrorClassObject, ErrorCodeObjectNotExist), nil
	}

	// 读取属性值
	value, err := targetObj.ReadProperty(propertyID)
	if err != nil || value == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedReadProperty, ErrorClassProperty, ErrorCodePropertyNotExist), nil
	}

	// 编码属性值
	encodedValue := encodeBACnetValue(value)

	// 构建ComplexAck响应
	header := []byte{
		BACnetAPDUTypeComplexAck | 0x01,    // APDU类型：复杂确认，服务确认
		0x00,                               // Reserved
		invokeID,                           // 与请求相同的invokeID
		byte(len(encodedValue) + 4),        // 复杂确认长度
		BACnetServiceConfirmedReadProperty, // 服务类型
	}

	// 添加上下文标签0，用于标识读取的属性值
	response := append(header, 0x0c) // 上下文标签0，长度为内容长度
	response = append(response, encodedValue...)

	return response, nil
}

// decodeBACnetValue 解码BACnet值
func decodeBACnetValue(data []byte) (interface{}, int, error) {
	if len(data) < 1 {
		return nil, 0, fmt.Errorf("数据太短，无法解码值")
	}

	switch data[0] {
	case 0x11: // BOOLEAN
		if len(data) < 2 {
			return nil, 0, fmt.Errorf("BOOLEAN值数据太短")
		}
		return data[1] != 0, 2, nil
	case 0x21: // UNSIGNED INTEGER 8
		if len(data) < 2 {
			return nil, 0, fmt.Errorf("UNSIGNED INTEGER 8值数据太短")
		}
		return uint8(data[1]), 2, nil
	case 0x22: // UNSIGNED INTEGER 16
		if len(data) < 3 {
			return nil, 0, fmt.Errorf("UNSIGNED INTEGER 16值数据太短")
		}
		return uint16(data[1])<<8 | uint16(data[2]), 3, nil
	case 0x23: // UNSIGNED INTEGER 32
		if len(data) < 5 {
			return nil, 0, fmt.Errorf("UNSIGNED INTEGER 32值数据太短")
		}
		return uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4]), 5, nil
	case 0x39: // REAL
		if len(data) < 5 {
			return nil, 0, fmt.Errorf("REAL值数据太短")
		}
		// 从IEEE 754格式转换
		uintBits := uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
		return math.Float32frombits(uintBits), 5, nil
	case 0x41: // CHARACTER STRING
		if len(data) < 2 {
			return nil, 0, fmt.Errorf("CHARACTER STRING值数据太短")
		}
		strLen := int(data[1])
		if len(data) < 2+strLen {
			return nil, 0, fmt.Errorf("CHARACTER STRING值长度不匹配")
		}
		return string(data[2 : 2+strLen]), 2 + strLen, nil
	default:
		return nil, 0, fmt.Errorf("不支持的数据类型: %02x", data[0])
	}
}

// handleWriteProperty 处理写入属性请求
func (s *BACnetServer) handleWriteProperty(data []byte, invokeID byte) ([]byte, error) {
	// 解析对象标识符
	objectID, offset, err := parseObjectIdentifier(data)
	if err != nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedWriteProperty, ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 解析属性标识符
	propertyID, newOffset, err := parsePropertyIdentifier(data[offset:])
	if err != nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedWriteProperty, ErrorClassService, ErrorCodeValueOutOfRange), nil
	}
	offset += newOffset

	// 解析优先级字段 - 按照BACnet协议实现
	// BACnet优先级范围: 0-16 (0=最高优先级, 16=默认优先级)
	priority := uint8(data[offset])
	offset += 1

	// 验证优先级值是否在有效范围内
	if priority > 16 {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedWriteProperty, ErrorClassProperty, ErrorCodeInvalidParameterDataType), nil
	}

	// 解码属性值
	value, _, err := decodeBACnetValue(data[offset:])
	if err != nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedWriteProperty, ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 查找对象
	var targetObj model.Object

	// 检查是否是设备对象本身
	if objectID.Type == model.ObjectTypeDevice && objectID.Instance == s.device.GetObjectIdentifier().Instance {
		targetObj = s.device
	} else {
		// 在设备的对象列表中查找
		targetObj = s.device.FindObject(objectID)
	}

	// 对象不存在
	if targetObj == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedWriteProperty, ErrorClassObject, ErrorCodeObjectNotExist), nil
	}

	// 按照BACnet协议实现优先级写入
	// 将targetObj断言为BACnetObject类型以使用WritePropertyWithPriority方法
	if bacnetObj, ok := targetObj.(*model.BACnetObject); ok {
		err = bacnetObj.WritePropertyWithPriority(propertyID, value, priority)
	} else {
		// 回退到标准WriteProperty（默认优先级16）
		err = targetObj.WriteProperty(propertyID, value)
	}

	if err != nil {
		// 属性不可写
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedWriteProperty, ErrorClassProperty, ErrorCodePropertyNotWritable), nil
	}

	// 构建SimpleAck响应
	response := []byte{
		BACnetAPDUTypeSimpleAck | 0x01,      // APDU类型：简单确认，服务确认
		0x00,                                // Reserved
		invokeID,                            // 与请求相同的invokeID
		0x04,                                // 服务确认长度
		BACnetServiceConfirmedWriteProperty, // 确认WriteProperty服务
		0x00, 0x00, 0x00,                    // 填充
	}

	return response, nil
}

// handleReadPropertyMultiple 处理读取多个属性请求
func (s *BACnetServer) handleReadPropertyMultiple(data []byte, invokeID byte) ([]byte, error) {
	// 解析请求中的对象和属性列表
	var responseValues []byte
	offset := 0

	// BACnet协议：处理多个对象，每个对象可有多个属性
	for offset < len(data) {
		// 开始一个新的对象的响应部分
		objectResponseStart := []byte{0x02} // 上下文标签2，表示一个对象规范
		responseValues = append(responseValues, objectResponseStart...)

		// 解析对象标识符
		objectID, objOffset, err := parseObjectIdentifier(data[offset:])
		if err != nil {
			return s.createErrorResponse(invokeID, BACnetServiceConfirmedReadPropertyMultiple, ErrorClassService, ErrorCodeValueOutOfRange), nil
		}
		offset += objOffset

		// 查找对象
		var targetObj model.Object
		if objectID.Type == model.ObjectTypeDevice && objectID.Instance == s.device.GetObjectIdentifier().Instance {
			targetObj = s.device
		} else {
			targetObj = s.device.FindObject(objectID)
		}

		// 编码对象标识符到响应
		encodedObjectID := encodeObjectIdentifier(objectID)
		responseValues = append(responseValues, encodedObjectID...)

		// 处理对象级错误
		if targetObj == nil {
			objectError := []byte{
				0x01,                    // 上下文标签1，表示错误
				0x02,                    // 错误类别
				ErrorCodeObjectNotExist, // 错误代码
			}
			responseValues = append(responseValues, objectError...)

			// 跳过该对象的所有属性
			for offset < len(data) && len(data[offset:]) >= 2 {
				if data[offset] == 0x08 { // 上下文标签8表示新对象
					break
				}
				// 尝试解析属性标识符来前进偏移量
				_, propOffset, _ := parsePropertyIdentifier(data[offset:])
				if propOffset > 0 {
					offset += propOffset
				} else {
					offset++ // 安全前进
				}
			}
			continue
		}

		// 解析并处理该对象的多个属性
		propertyCount := 0
		propertyResponses := []byte{}

		for offset < len(data) && len(data[offset:]) >= 2 {
			// 检查是否是新对象开始或数据结束
			if offset+1 < len(data) && data[offset] == 0x08 && data[offset+1] == 0x03 {
				break // 遇到下一个对象
			}

			// 解析属性标识符
			propID, propOffset, err := parsePropertyIdentifier(data[offset:])
			if err != nil || propOffset == 0 {
				break
			}
			offset += propOffset

			// 属性响应开始
			propertyResponse := []byte{0x00} // 上下文标签0，表示属性响应

			// 读取属性值
			value, err := targetObj.ReadProperty(propID)
			if err != nil || value == nil {
				// 属性不存在，添加错误信息
				errorInfo := []byte{
					0x01,                      // 上下文标签1，表示错误
					0x02,                      // 错误类别
					ErrorCodePropertyNotExist, // 错误代码
				}
				propertyResponse = append(propertyResponse, errorInfo...)
			} else {
				// 编码属性标识符
				propertyResponse = append(propertyResponse, encodePropertyIdentifier(propID)...)

				// 属性存在，编码并添加值
				encodedValue := encodeBACnetValue(value)
				propertyResponse = append(propertyResponse, encodedValue...)
			}

			propertyResponses = append(propertyResponses, propertyResponse...)
			propertyCount++
		}

		// 添加属性响应计数和响应数据
		if propertyCount > 0 {
			// 按照BACnet协议规范：上下文标签3后添加长度字节
			propertyListHeader := []byte{
				0x03,                         // 上下文标签3，表示属性列表
				byte(len(propertyResponses)), // 长度字节，表示后续属性响应数据的长度
			}
			responseValues = append(responseValues, propertyListHeader...)
			responseValues = append(responseValues, propertyResponses...)
		} else {
			// 没有属性响应时，添加空的属性列表
			emptyPropertyList := []byte{
				0x03, // 上下文标签3
				0x00, // 长度为0
			}
			responseValues = append(responseValues, emptyPropertyList...)
		}
	}

	// 构建ComplexAck响应
	header := []byte{
		BACnetAPDUTypeComplexAck | 0x01, // APDU类型：复杂确认，服务确认
		0x00,                            // Reserved
		invokeID,                        // 与请求相同的invokeID
		byte(len(responseValues) + 4),   // 复杂确认长度
		BACnetServiceConfirmedReadPropertyMultiple, // 服务类型
	}

	response := append(header, responseValues...)
	return response, nil
}

// parseWriteAccessSpec 解析写入访问规范
func parseWriteAccessSpec(data []byte) (model.ObjectIdentifier, []struct {
	PropertyID model.PropertyIdentifier
	Value      interface{}
	Priority   uint8
}, int, error) {
	var offset int

	// 检查数据长度是否足够
	if len(data) < 3 {
		return model.ObjectIdentifier{}, nil, 0, errors.New("insufficient data for write access spec")
	}

	// 解析对象标识符
	objectID, objOffset, err := parseObjectIdentifier(data)
	if err != nil {
		return model.ObjectIdentifier{}, nil, 0, fmt.Errorf("failed to parse object identifier: %w", err)
	}
	offset += objOffset

	// 解析属性值对列表
	var propertyValues []struct {
		PropertyID model.PropertyIdentifier
		Value      interface{}
		Priority   uint8
	}

	// 按照BACnet协议规范解析属性值对列表
	for offset < len(data) {
		// 检查剩余数据是否足够
		if len(data[offset:]) < 3 {
			break
		}

		// 解析属性标识符
		propID, propOffset, err := parsePropertyIdentifier(data[offset:])
		if err != nil {
			return model.ObjectIdentifier{}, propertyValues, offset, fmt.Errorf("failed to parse property identifier: %w", err)
		}
		offset += propOffset

		// 解析优先级字段（BACnet Priority，1字节）
		if offset >= len(data) {
			return model.ObjectIdentifier{}, propertyValues, offset, errors.New("incomplete priority field")
		}
		priority := uint8(data[offset])
		offset += 1

		// 解码属性值
		if offset < len(data) {
			value, valOffset, err := decodeBACnetValue(data[offset:])
			if err != nil {
				return model.ObjectIdentifier{}, propertyValues, offset, fmt.Errorf("failed to decode property value: %w", err)
			}
			offset += valOffset

			// 添加到属性值列表
			propertyValues = append(propertyValues, struct {
				PropertyID model.PropertyIdentifier
				Value      interface{}
				Priority   uint8
			}{propID, value, priority})
		} else {
			return model.ObjectIdentifier{}, propertyValues, offset, errors.New("missing property value")
		}
	}

	return objectID, propertyValues, offset, nil
}

// createWritePropertyMultipleErrorResponse 创建WritePropertyMultiple错误响应
func (s *BACnetServer) createWritePropertyMultipleErrorResponse(invokeID byte, writeAccessSpecs []struct {
	ObjectID       model.ObjectIdentifier
	PropertyErrors []struct {
		PropertyID model.PropertyIdentifier
		ErrorClass byte
		ErrorCode  byte
	}
}) []byte {
	// 创建ComplexAck响应
	response := []byte{
		BACnetAPDUTypeComplexAck | 0x01, // APDU类型：复杂确认，服务确认
		0x00,                            // Reserved
		invokeID,                        // 与请求相同的invokeID
		0x00,                            // 长度占位符，后面会更新
		BACnetServiceConfirmedWritePropertyMultiple, // 服务类型
	}

	// 添加错误信息
	for _, spec := range writeAccessSpecs {
		// 添加对象标识符
		objectIDBytes := []byte{
			byte(spec.ObjectID.Type >> 16),
			byte(spec.ObjectID.Type >> 8),
			byte(spec.ObjectID.Instance >> 8),
			byte(spec.ObjectID.Instance),
		}
		response = append(response, objectIDBytes...)

		// 添加属性错误列表
		for _, propErr := range spec.PropertyErrors {
			// 添加属性标识符
			propertyIDBytes := []byte{
				byte(propErr.PropertyID >> 8),
				byte(propErr.PropertyID),
			}
			response = append(response, propertyIDBytes...)

			// 添加错误信息
			errorInfo := []byte{
				0x11, // 上下文标签1，表示错误
				propErr.ErrorClass,
				propErr.ErrorCode,
			}
			response = append(response, errorInfo...)
		}
	}

	// 更新长度字段
	response[3] = byte(len(response) - 4)

	return response
}

// handleWritePropertyMultiple 处理写入多个属性请求
func (s *BACnetServer) handleWritePropertyMultiple(data []byte, invokeID byte) ([]byte, error) {
	var offset int
	var hasErrors bool
	var errorSpecs []struct {
		ObjectID       model.ObjectIdentifier
		PropertyErrors []struct {
			PropertyID model.PropertyIdentifier
			ErrorClass byte
			ErrorCode  byte
		}
	}

	// 解析请求中的所有写入访问规范
	for offset < len(data) {
		// 解析写入访问规范
		objectID, propertyValues, specOffset, err := parseWriteAccessSpec(data[offset:])
		if err != nil {
			break
		}
		offset += specOffset

		// 查找目标对象
		var targetObj model.Object
		if objectID.Type == model.ObjectTypeDevice && objectID.Instance == s.device.GetObjectIdentifier().Instance {
			targetObj = s.device
		} else {
			targetObj = s.device.FindObject(objectID)
		}

		// 处理每个属性的写入
		spec := struct {
			ObjectID       model.ObjectIdentifier
			PropertyErrors []struct {
				PropertyID model.PropertyIdentifier
				ErrorClass byte
				ErrorCode  byte
			}
		}{ObjectID: objectID}

		objectExists := targetObj != nil

		for _, propVal := range propertyValues {
			var errorClass, errorCode byte

			if !objectExists {
				// 对象不存在
				errorClass = ErrorClassObject
				errorCode = ErrorCodeObjectNotExist
			} else {
				// 尝试写入属性
				var err error

				// 使用默认优先级16写入（简化处理）
				if bacnetObj, ok := targetObj.(*model.BACnetObject); ok {
					err = bacnetObj.WritePropertyWithPriority(propVal.PropertyID, propVal.Value, 16)
				} else {
					err = targetObj.WriteProperty(propVal.PropertyID, propVal.Value)
				}

				// 检查写入错误
				if err != nil {
					errorClass = ErrorClassProperty
					errorCode = ErrorCodePropertyNotWritable
				}
			}

			// 如果有错误，添加到错误规范中
			if errorClass != 0 {
				hasErrors = true
				spec.PropertyErrors = append(spec.PropertyErrors, struct {
					PropertyID model.PropertyIdentifier
					ErrorClass byte
					ErrorCode  byte
				}{propVal.PropertyID, errorClass, errorCode})
			}
		}

		// 如果有错误，添加到错误规范列表
		if len(spec.PropertyErrors) > 0 {
			errorSpecs = append(errorSpecs, spec)
		}
	}

	if hasErrors {
		// 有错误，返回包含错误信息的ComplexAck响应
		return s.createWritePropertyMultipleErrorResponse(invokeID, errorSpecs), nil
	} else {
		// 全部成功，返回SimpleAck响应
		response := []byte{
			BACnetAPDUTypeSimpleAck | 0x01, // APDU类型：简单确认，服务确认
			0x00,                           // Reserved
			invokeID,                       // 与请求相同的invokeID
			0x04,                           // 服务确认长度
			BACnetServiceConfirmedWritePropertyMultiple, // 确认WritePropertyMultiple服务
			0x00, 0x00, 0x00, // 填充
		}
		return response, nil
	}
}

// 告警状态常量
const (
	EventStateNormal        = 0x00 // 正常
	EventStateFault         = 0x01 // 故障
	EventStateOffnormal     = 0x02 // 异常
	EventStateHighAlarm     = 0x03 // 高告警
	EventStateLowAlarm      = 0x04 // 低告警
	EventStateHighHighAlarm = 0x05 // 高高告警
	EventStateLowLowAlarm   = 0x06 // 低低告警
)

// 解析告警确认请求数据
func parseAcknowledgeAlarmData(data []byte) (model.ObjectIdentifier, uint32, uint32, uint32, error) {
	if len(data) < 16 {
		return model.ObjectIdentifier{}, 0, 0, 0, fmt.Errorf("数据太短，无法解析告警确认请求")
	}

	// 解析告警源对象标识符
	objectID, _, err := parseObjectIdentifier(data)
	if err != nil {
		return model.ObjectIdentifier{}, 0, 0, 0, err
	}

	// 按照BACnet协议规范解析告警代码
	// 告警代码以4字节无符号整数的形式表示，遵循大端字节序
	alarmCode := uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])

	// 按照BACnet协议规范解析告警类型
	// 告警类型以4字节无符号整数的形式表示，遵循大端字节序
	alarmType := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])

	// 按照BACnet协议规范解析确认时间戳
	// 时间戳以4字节无符号整数的形式表示，遵循大端字节序
	timeStamp := uint32(data[12])<<24 | uint32(data[13])<<16 | uint32(data[14])<<8 | uint32(data[15])

	return objectID, alarmCode, alarmType, timeStamp, nil
}

// handleAcknowledgeAlarm 处理告警确认请求
func (s *BACnetServer) handleAcknowledgeAlarm(data []byte, invokeID byte) ([]byte, error) {
	// 解析告警确认请求数据
	objectID, alarmCode, alarmType, timeStamp, err := parseAcknowledgeAlarmData(data)
	if err != nil {
		// 数据格式错误
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAcknowledgeAlarm,
			ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 查找对应的对象
	var targetObj model.Object
	if objectID.Type == model.ObjectTypeDevice && objectID.Instance == s.device.GetObjectIdentifier().Instance {
		targetObj = s.device
	} else {
		// 在设备的对象列表中查找
		targetObj = s.device.FindObject(objectID)
	}

	// 对象不存在
	if targetObj == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAcknowledgeAlarm,
			ErrorClassObject, ErrorCodeObjectNotExist), nil
	}

	// 更新对象的告警状态
	// 1. 将事件状态设置为正常
	targetObj.WriteProperty(model.PropertyIdentifierEventState, EventStateNormal)

	// 2. 清除状态标志中的告警标志
	if obj, ok := targetObj.(*model.BACnetObject); ok {
		flags := obj.GetStatusFlags()
		flags &^= model.StatusFlagInAlarm // 清除告警标志
		obj.SetStatusFlags(flags)
	}

	// 3. 记录告警确认信息
	fmt.Printf("告警确认处理: 对象=%s, 告警代码=0x%08x, 告警类型=0x%08x, 时间戳=%d\n",
		targetObj.GetObjectName(), alarmCode, alarmType, timeStamp)

	// 构建SimpleAck响应
	response := []byte{
		BACnetAPDUTypeSimpleAck | 0x01,         // APDU类型：简单确认，服务确认
		0x00,                                   // Reserved
		invokeID,                               // 与请求相同的invokeID
		0x04,                                   // 服务确认长度
		BACnetServiceConfirmedAcknowledgeAlarm, // 确认AcknowledgeAlarm服务
		0x00, 0x00, 0x00,                       // 填充
	}

	return response, nil
}

// 文件读取请求结构
type FileReadRequest struct {
	FileID      model.ObjectIdentifier
	StartOffset uint32
	ReadCount   uint32
}

// 文件写入请求结构
type FileWriteRequest struct {
	FileID      model.ObjectIdentifier
	StartOffset uint32
	WriteData   []byte
}

// 文件删除请求结构
type FileDeleteRequest struct {
	FileID model.ObjectIdentifier
}

// 解析文件读取请求
func parseFileReadRequest(data []byte) (FileReadRequest, error) {
	if len(data) < 12 {
		return FileReadRequest{}, fmt.Errorf("数据太短，无法解析文件读取请求")
	}

	// 解析文件对象标识符
	fileID, offset, err := parseObjectIdentifier(data)
	if err != nil {
		return FileReadRequest{}, err
	}

	// 按照BACnet协议规范解析起始偏移量
	// 起始偏移量以4字节无符号整数的形式表示，遵循大端字节序
	startOffset := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])

	// 按照BACnet协议规范解析读取数量
	// 读取数量以4字节无符号整数的形式表示，遵循大端字节序
	readCount := uint32(data[offset+4])<<24 | uint32(data[offset+5])<<16 | uint32(data[offset+6])<<8 | uint32(data[offset+7])

	return FileReadRequest{
		FileID:      fileID,
		StartOffset: startOffset,
		ReadCount:   readCount,
	}, nil
}

// 解析文件写入请求
func parseFileWriteRequest(data []byte) (FileWriteRequest, error) {
	if len(data) < 16 {
		return FileWriteRequest{}, fmt.Errorf("数据太短，无法解析文件写入请求")
	}

	// 解析文件对象标识符
	fileID, offset, err := parseObjectIdentifier(data)
	if err != nil {
		return FileWriteRequest{}, err
	}

	// 按照BACnet协议规范解析起始偏移量
	// 起始偏移量以4字节无符号整数的形式表示，遵循大端字节序
	startOffset := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])

	// 按照BACnet协议规范解析写入数据长度
	// 写入数据长度以4字节无符号整数的形式表示，遵循大端字节序
	dataLength := uint32(data[offset+4])<<24 | uint32(data[offset+5])<<16 | uint32(data[offset+6])<<8 | uint32(data[offset+7])

	// 按照BACnet协议规范进行数据边界检查
	// 确保写入数据长度不超出请求范围，避免缓冲区溢出
	if offset+8+int(dataLength) > len(data) {
		return FileWriteRequest{}, fmt.Errorf("写入数据长度超出请求范围")
	}

	writeData := data[offset+8 : offset+8+int(dataLength)]

	return FileWriteRequest{
		FileID:      fileID,
		StartOffset: startOffset,
		WriteData:   writeData,
	}, nil
}

// 解析文件删除请求
func parseFileDeleteRequest(data []byte) (FileDeleteRequest, error) {
	if len(data) < 4 {
		return FileDeleteRequest{}, fmt.Errorf("数据太短，无法解析文件删除请求")
	}

	// 解析文件对象标识符
	fileID, _, err := parseObjectIdentifier(data)
	if err != nil {
		return FileDeleteRequest{}, err
	}

	return FileDeleteRequest{
		FileID: fileID,
	}, nil
}

// handleAtomicReadFile 处理文件读取请求
func (s *BACnetServer) handleAtomicReadFile(data []byte, invokeID byte) ([]byte, error) {
	// 解析文件读取请求
	request, err := parseFileReadRequest(data)
	if err != nil {
		// 数据格式错误
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAtomicReadFile,
			ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 查找文件对象
	fileObj := s.device.FindObject(request.FileID)
	if fileObj == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAtomicReadFile,
			ErrorClassObject, ErrorCodeObjectNotExist), nil
	}

	// 类型断言为BACnetFile
	bacFile, ok := fileObj.(*model.BACnetFile)
	if !ok {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAtomicReadFile,
			ErrorClassObject, ErrorCodeInvalidDataType), nil
	}

	// 读取文件数据
	fileData, err := bacFile.ReadFile(request.StartOffset, request.ReadCount)
	if err != nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAtomicReadFile,
			ErrorClassFile, ErrorCodeFileAccessDenied), nil
	}

	// 构建ComplexAck响应
	response := []byte{
		BACnetAPDUTypeComplexAck | 0x01,      // APDU类型：复杂确认，服务确认
		0x00,                                 // Reserved
		invokeID,                             // 与请求相同的invokeID
		byte(len(fileData) + 9),              // 服务确认长度
		BACnetServiceConfirmedAtomicReadFile, // 确认AtomicReadFile服务
		0x02,                                 // 标记文件读取数据
		0x04,                                 // 起始偏移量长度
		byte(request.StartOffset >> 24),      // 起始偏移量
		byte(request.StartOffset >> 16),
		byte(request.StartOffset >> 8),
		byte(request.StartOffset),
		0x04,                      // 数据长度
		byte(len(fileData) >> 24), // 数据长度值
		byte(len(fileData) >> 16),
		byte(len(fileData) >> 8),
		byte(len(fileData)),
	}

	// 添加实际文件数据
	response = append(response, fileData...)

	fmt.Printf("文件读取: 对象=%s, 偏移量=%d, 读取字节数=%d\n",
		fileObj.GetObjectName(), request.StartOffset, len(fileData))

	return response, nil
}

// handleAtomicWriteFile 处理文件写入请求
func (s *BACnetServer) handleAtomicWriteFile(data []byte, invokeID byte) ([]byte, error) {
	// 解析文件写入请求
	request, err := parseFileWriteRequest(data)
	if err != nil {
		// 数据格式错误
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAtomicWriteFile,
			ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 查找文件对象
	fileObj := s.device.FindObject(request.FileID)
	if fileObj == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAtomicWriteFile,
			ErrorClassObject, ErrorCodeObjectNotExist), nil
	}

	// 类型断言为BACnetFile
	bacFile, ok := fileObj.(*model.BACnetFile)
	if !ok {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAtomicWriteFile,
			ErrorClassObject, ErrorCodeInvalidDataType), nil
	}

	// 写入文件数据
	err = bacFile.WriteFile(request.StartOffset, request.WriteData)
	if err != nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedAtomicWriteFile,
			ErrorClassFile, ErrorCodeFileAccessDenied), nil
	}

	// 构建SimpleAck响应
	response := []byte{
		BACnetAPDUTypeSimpleAck | 0x01,        // APDU类型：简单确认，服务确认
		0x00,                                  // Reserved
		invokeID,                              // 与请求相同的invokeID
		0x04,                                  // 服务确认长度
		BACnetServiceConfirmedAtomicWriteFile, // 确认AtomicWriteFile服务
		0x00, 0x00, 0x00,                      // 填充
	}

	fmt.Printf("文件写入: 对象=%s, 偏移量=%d, 写入字节数=%d, 文件大小=%d\n",
		fileObj.GetObjectName(), request.StartOffset, len(request.WriteData), len(bacFile.FileData))

	return response, nil
}

// handleDeleteFile 处理文件删除请求
func (s *BACnetServer) handleDeleteFile(data []byte, invokeID byte) ([]byte, error) {
	// 解析文件删除请求
	request, err := parseFileDeleteRequest(data)
	if err != nil {
		// 数据格式错误
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedDeleteFile,
			ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 查找文件对象
	fileObj := s.device.FindObject(request.FileID)
	if fileObj == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedDeleteFile,
			ErrorClassObject, ErrorCodeObjectNotExist), nil
	}

	// 类型断言为BACnetFile
	bacFile, ok := fileObj.(*model.BACnetFile)
	if !ok {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedDeleteFile,
			ErrorClassObject, ErrorCodeInvalidDataType), nil
	}

	// 删除文件内容
	err = bacFile.DeleteFile()
	if err != nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedDeleteFile,
			ErrorClassFile, ErrorCodeFileAccessDenied), nil
	}

	// 构建SimpleAck响应
	response := []byte{
		BACnetAPDUTypeSimpleAck | 0x01,   // APDU类型：简单确认，服务确认
		0x00,                             // Reserved
		invokeID,                         // 与请求相同的invokeID
		0x04,                             // 服务确认长度
		BACnetServiceConfirmedDeleteFile, // 确认DeleteFile服务
		0x00, 0x00, 0x00,                 // 填充
	}

	fmt.Printf("文件删除: 对象=%s\n", fileObj.GetObjectName())

	return response, nil
}

// SubscribeCOVRequest 订阅变化通知请求结构
type SubscribeCOVRequest struct {
	ObjectID            model.ObjectIdentifier
	SubscribeToAll      bool
	Lifetime            uint32
	IssueConfirmedNotif bool
	SubscriberProcessID uint32
	SubscriberDeviceID  model.ObjectIdentifier
	InitiatingDeviceID  model.ObjectIdentifier
}

// SubscribeCOVPropertyRequest 属性订阅变化通知请求结构
type SubscribeCOVPropertyRequest struct {
	ObjectID            model.ObjectIdentifier
	Lifetime            uint32
	IssueConfirmedNotif bool
	PropertyReferences  []model.PropertyIdentifier
	SubscriberProcessID uint32
	SubscriberDeviceID  model.ObjectIdentifier
	InitiatingDeviceID  model.ObjectIdentifier
}

// 解析SubscribeCOV请求
func parseSubscribeCOVRequest(data []byte) (SubscribeCOVRequest, error) {
	if len(data) < 16 {
		return SubscribeCOVRequest{}, fmt.Errorf("数据太短，无法解析SubscribeCOV请求")
	}

	// 解析对象标识符
	objectID, offset, err := parseObjectIdentifier(data)
	if err != nil {
		return SubscribeCOVRequest{}, err
	}

	// 解析订阅标志位
	subscribeToAll := (data[offset] & 0x01) == 0x01
	offset++

	// 解析生命周期
	lifetime := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
	offset += 4

	// 解析确认通知标志
	issueConfirmedNotif := false
	if len(data) > offset {
		issueConfirmedNotif = (data[offset] & 0x01) == 0x01
	}

	// 模拟的订阅者和发起设备信息
	subscriberProcessID := uint32(1)
	subscriberDeviceID := model.ObjectIdentifier{Type: model.ObjectTypeDevice, Instance: 1}
	initiatingDeviceID := model.ObjectIdentifier{Type: model.ObjectTypeDevice, Instance: 1}

	return SubscribeCOVRequest{
		ObjectID:            objectID,
		SubscribeToAll:      subscribeToAll,
		Lifetime:            lifetime,
		IssueConfirmedNotif: issueConfirmedNotif,
		SubscriberProcessID: subscriberProcessID,
		SubscriberDeviceID:  subscriberDeviceID,
		InitiatingDeviceID:  initiatingDeviceID,
	}, nil
}

// 解析SubscribeCOVProperty请求
func parseSubscribeCOVPropertyRequest(data []byte) (SubscribeCOVPropertyRequest, error) {
	if len(data) < 16 {
		return SubscribeCOVPropertyRequest{}, fmt.Errorf("数据太短，无法解析SubscribeCOVProperty请求")
	}

	// 声明所有变量，确保作用域正确
	var (
		objectID            model.ObjectIdentifier
		offset              int
		err                 error
		lifetime            uint32
		issueConfirmedNotif bool
		propertyReferences  []model.PropertyIdentifier
		subscriberProcessID uint32
		subscriberDeviceID  model.ObjectIdentifier
		initiatingDeviceID  model.ObjectIdentifier
	)

	// 解析对象标识符
	objectID, offset, err = parseObjectIdentifier(data)
	if err != nil {
		return SubscribeCOVPropertyRequest{}, err
	}

	// 解析生命周期
	lifetime = uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
	offset += 4

	// 解析确认通知标志
	issueConfirmedNotif = false
	if len(data) > offset {
		issueConfirmedNotif = (data[offset] & 0x01) == 0x01
		offset++
	}

	// 按照BACnet协议规范解析PropertyReferences
	// 根据BACnet协议，PropertyReferences是一个可选参数，包含要监控的属性列表
	// 如果存在PropertyReferences参数，它会以上下文标记开始
	if offset < len(data) {
		// 检查是否有PropertyReferences上下文标记
		contextTag := data[offset]
		if (contextTag & 0xE0) == 0xA0 { // 上下文标记编号3 (0xA0-0xBF表示上下文标记)
			offset++
			// 读取属性引用列表的长度
			if offset < len(data) {
				propertyListCount := int(data[offset])
				offset++

				// 解析每个属性引用
				propertyReferences = make([]model.PropertyIdentifier, 0, propertyListCount)
				for i := 0; i < propertyListCount && offset+2 <= len(data); i++ {
					// 解析属性标识符
					propertyID := model.PropertyIdentifier(data[offset])
					offset += 2 // 跳过属性类型和保留字节
					propertyReferences = append(propertyReferences, propertyID)
				}
			}
		}
	}

	// 按照BACnet协议规范解析订阅者进程ID
	if offset+4 <= len(data) {
		subscriberProcessID = uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		offset += 4
	}

	// 按照BACnet协议规范解析订阅者设备ID
	if offset+3 <= len(data) {
		subscriberDeviceID, offset, err = parseObjectIdentifier(data[offset:])
		if err != nil {
			// 如果解析失败，使用默认值
			subscriberDeviceID = model.ObjectIdentifier{Type: model.ObjectTypeDevice, Instance: 1}
		}
	}

	// 按照BACnet协议规范解析发起设备ID
	if offset+3 <= len(data) {
		initiatingDeviceID, _, err = parseObjectIdentifier(data[offset:])
		if err != nil {
			// 如果解析失败，使用默认值
			initiatingDeviceID = model.ObjectIdentifier{Type: model.ObjectTypeDevice, Instance: 1}
		}
	}

	return SubscribeCOVPropertyRequest{
		ObjectID:            objectID,
		Lifetime:            lifetime,
		IssueConfirmedNotif: issueConfirmedNotif,
		PropertyReferences:  propertyReferences,
		SubscriberProcessID: subscriberProcessID,
		SubscriberDeviceID:  subscriberDeviceID,
		InitiatingDeviceID:  initiatingDeviceID,
	}, nil
}

// 全局原子计数器，用于生成唯一的订阅ID
var subscriptionCounter uint32

// 生成唯一的订阅ID
func generateSubscriptionID() uint32 {
	// 结合时间戳的高32位和原子递增计数器，减少冲突可能性
	timestamp := uint32(time.Now().UnixNano() >> 32)
	counter := atomic.AddUint32(&subscriptionCounter, 1)
	// 通过位运算组合时间戳和计数器，生成更唯一的ID
	return (timestamp & 0xFFFF0000) | (counter & 0x0000FFFF)
}

// handleSubscribeCOV 处理订阅变化通知请求
func (s *BACnetServer) handleSubscribeCOV(data []byte, invokeID byte) ([]byte, error) {
	// 解析订阅请求
	request, err := parseSubscribeCOVRequest(data)
	if err != nil {
		// 数据格式错误
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedSubscribeCOV,
			ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 查找目标对象
	targetObj := s.device.FindObject(request.ObjectID)
	if targetObj == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedSubscribeCOV,
			ErrorClassObject, ErrorCodeObjectNotExist), nil
	}

	// 类型断言为BACnetObject
	bacObj, ok := targetObj.(*model.BACnetObject)
	if !ok {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedSubscribeCOV,
			ErrorClassCov, ErrorCodeCovObject), nil
	}

	// 生成订阅ID
	subscriptionID := generateSubscriptionID()

	// 创建订阅对象
	subscription := model.COVSubscription{
		SubscriptionID:                 subscriptionID,
		DeviceID:                       s.device.GetObjectIdentifier().Instance,
		ObjectIdentifier:               request.ObjectID,
		Lifetime:                       request.Lifetime,
		IssueConfirmedCOVNotifications: request.IssueConfirmedNotif,
		MonitoredProperties:            []model.PropertyIdentifier{}, // 空列表表示监控所有属性
		Timestamp:                      time.Now(),
		ClientAddress:                  s.currentClientAddr,
	}

	// 添加订阅
	bacObj.AddCOVSubscription(subscription)

	// 构建ComplexAck响应，包含订阅ID
	response := []byte{
		BACnetAPDUTypeComplexAck | 0x01,    // APDU类型：复杂确认
		0x00,                               // Reserved
		invokeID,                           // 与请求相同的invokeID
		0x08,                               // 服务确认长度
		BACnetServiceConfirmedSubscribeCOV, // 确认SubscribeCOV服务
		0x04,                               // 标记订阅ID
		byte(subscriptionID >> 24),         // 订阅ID值
		byte(subscriptionID >> 16),
		byte(subscriptionID >> 8),
		byte(subscriptionID),
	}

	fmt.Printf("创建COV订阅: 订阅ID=%d, 对象=%s, 生命周期=%d秒, 监控所有属性=%v\n",
		subscriptionID, targetObj.GetObjectName(), request.Lifetime, request.SubscribeToAll)

	return response, nil
}

// handleSubscribeCOVProperty 处理属性订阅变化通知请求
func (s *BACnetServer) handleSubscribeCOVProperty(data []byte, invokeID byte) ([]byte, error) {
	// 解析属性订阅请求
	request, err := parseSubscribeCOVPropertyRequest(data)
	if err != nil {
		// 数据格式错误
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedSubscribeCOVProperty,
			ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 查找目标对象
	targetObj := s.device.FindObject(request.ObjectID)
	if targetObj == nil {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedSubscribeCOVProperty,
			ErrorClassObject, ErrorCodeObjectNotExist), nil
	}

	// 类型断言为BACnetObject
	bacObj, ok := targetObj.(*model.BACnetObject)
	if !ok {
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedSubscribeCOVProperty,
			ErrorClassCov, ErrorCodeCovObject), nil
	}

	// 检查属性是否存在
	for _, prop := range request.PropertyReferences {
		_, err := targetObj.ReadProperty(prop)
		if err != nil {
			return s.createErrorResponse(invokeID, BACnetServiceConfirmedSubscribeCOVProperty,
				ErrorClassCov, ErrorCodeCovProperty), nil
		}
	}

	// 生成订阅ID
	subscriptionID := generateSubscriptionID()

	// 创建属性订阅对象
	subscription := model.COVSubscription{
		SubscriptionID:                 subscriptionID,
		DeviceID:                       s.device.GetObjectIdentifier().Instance,
		ObjectIdentifier:               request.ObjectID,
		Lifetime:                       request.Lifetime,
		IssueConfirmedCOVNotifications: request.IssueConfirmedNotif,
		MonitoredProperties:            request.PropertyReferences,
		Timestamp:                      time.Now(),
		ClientAddress:                  s.currentClientAddr,
	}

	// 添加订阅
	bacObj.AddCOVSubscription(subscription)

	// 构建ComplexAck响应，包含订阅ID
	response := []byte{
		BACnetAPDUTypeComplexAck | 0x01, // APDU类型：复杂确认
		0x00,                            // Reserved
		invokeID,                        // 与请求相同的invokeID
		0x08,                            // 服务确认长度
		BACnetServiceConfirmedSubscribeCOVProperty, // 确认SubscribeCOVProperty服务
		0x04,                       // 标记订阅ID
		byte(subscriptionID >> 24), // 订阅ID值
		byte(subscriptionID >> 16),
		byte(subscriptionID >> 8),
		byte(subscriptionID),
	}

	// 记录监控的属性列表
	propNames := []string{}
	for _, prop := range request.PropertyReferences {
		propNames = append(propNames, fmt.Sprintf("%d", prop))
	}

	fmt.Printf("创建属性COV订阅: 订阅ID=%d, 对象=%s, 生命周期=%d秒, 监控属性=%v\n",
		subscriptionID, targetObj.GetObjectName(), request.Lifetime, propNames)

	return response, nil
}

// CancelCOVSubscriptionRequest 取消订阅变化通知请求结构
type CancelCOVSubscriptionRequest struct {
	SubscriberProcessID uint32
	SubscriberDeviceID  model.ObjectIdentifier
	InitiatingDeviceID  model.ObjectIdentifier
	SubscriptionID      uint32
}

// 解析取消订阅请求
func parseCancelCOVSubscriptionRequest(data []byte) (CancelCOVSubscriptionRequest, error) {
	if len(data) < 4 {
		return CancelCOVSubscriptionRequest{}, fmt.Errorf("数据太短，无法解析CancelCOVSubscription请求")
	}

	// 声明所有变量，确保作用域正确
	var (
		subscriptionID      uint32
		subscriberProcessID uint32
		subscriberDeviceID  model.ObjectIdentifier
		initiatingDeviceID  model.ObjectIdentifier
		offset              int
		err                 error
	)

	// 解析订阅ID
	subscriptionID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	offset = 4

	// 按照BACnet协议规范解析可选参数
	// 解析订阅者进程ID (上下文标记1)
	if offset < len(data) && (data[offset]&0xE0) == 0xA0 {
		offset++ // 跳过上下文标记
		if offset+4 <= len(data) {
			subscriberProcessID = uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
			offset += 4
		}
	}

	// 解析订阅者设备ID (上下文标记2)
	if offset < len(data) && (data[offset]&0xE0) == 0xA0 {
		offset++ // 跳过上下文标记
		if offset+3 <= len(data) {
			subscriberDeviceID, offset, err = parseObjectIdentifier(data[offset:])
			if err != nil {
				// 如果解析失败，使用默认值
				subscriberDeviceID = model.ObjectIdentifier{Type: model.ObjectTypeDevice, Instance: 1}
			}
		}
	}

	// 解析发起设备ID (上下文标记3)
	if offset < len(data) && (data[offset]&0xE0) == 0xA0 {
		offset++ // 跳过上下文标记
		if offset+3 <= len(data) {
			initiatingDeviceID, _, err = parseObjectIdentifier(data[offset:])
			if err != nil {
				// 如果解析失败，使用默认值
				initiatingDeviceID = model.ObjectIdentifier{Type: model.ObjectTypeDevice, Instance: 1}
			}
		}
	}

	return CancelCOVSubscriptionRequest{
		SubscriberProcessID: subscriberProcessID,
		SubscriberDeviceID:  subscriberDeviceID,
		InitiatingDeviceID:  initiatingDeviceID,
		SubscriptionID:      subscriptionID,
	}, nil
}

// handleCancelCOVSubscription 处理取消订阅变化通知请求
func (s *BACnetServer) handleCancelCOVSubscription(data []byte, invokeID byte) ([]byte, error) {
	// 解析取消订阅请求
	request, err := parseCancelCOVSubscriptionRequest(data)
	if err != nil {
		// 数据格式错误
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedCancelCOVSubscription,
			ErrorClassService, ErrorCodeValueOutOfRange), nil
	}

	// 记录处理日志
	fmt.Printf("处理取消COV订阅请求: 订阅ID=%d\n", request.SubscriptionID)

	// 查找并移除订阅
	found := false
	// 遍历设备中的所有对象
	for _, obj := range s.device.Objects {
		// 尝试类型断言为BACnetObject以访问RemoveCOVSubscription方法
		if bacnetObj, ok := obj.(*model.BACnetObject); ok {
			// 调用RemoveCOVSubscription方法移除订阅
			if bacnetObj.RemoveCOVSubscription(request.SubscriptionID) {
				found = true
				fmt.Printf("成功从对象 %s 中移除订阅ID=%d\n",
					bacnetObj.GetObjectName(), request.SubscriptionID)
				// 一旦找到就可以退出循环，因为订阅ID应该是全局唯一的
				break
			}
		}
	}

	// 检查订阅是否存在
	if !found {
		// 订阅不存在，返回Cov类错误
		return s.createErrorResponse(invokeID, BACnetServiceConfirmedCancelCOVSubscription,
			ErrorClassCov, ErrorCodeCovObject), nil
	}

	// 构建SimpleAck响应
	response := []byte{
		BACnetAPDUTypeSimpleAck | 0x01,
		0x00,
		invokeID,
		0x04,
		BACnetServiceConfirmedCancelCOVSubscription,
		0x00, 0x00, 0x00,
	}

	return response, nil
}

// createIAmResponse 创建I-Am响应消息
func (s *BACnetServer) createIAmResponse() []byte {
	if s.device == nil {
		return nil
	}

	// 获取设备信息
	deviceObjID := s.device.GetObjectIdentifier()
	deviceID := deviceObjID.Instance

	// BACnet协议常量
	const (
		BVLCTypeOriginalUnicast     = 0x81 // 原始单播BVLC
		BVLCOriginalUnicastNPDU     = 0x0a // 原始单播NPDU功能码
		NPDUVersion1                = 0x01 // NPDU版本1
		NPDUControlUnsegmented      = 0x04 // 未分段NPDU控制字节
		APDUTypeUnconfirmedService  = 0x00 // 未确认服务APDU类型
		BACnetServiceUnconfirmedIAm = 0x08 // I-Am服务码
		MaxAPDUSize1024Bytes        = 0x04 // 最大APDU大小1024字节
		SegmentationNo              = 0x00 // 不支持分段
		VendorIDDefault             = 0x00 // 默认厂商ID
	)

	// 计算消息长度
	totalLength := 26 // BVLC(4) + NPDU(7) + APDU头部(4) + I-Am服务数据(11)

	// 构建完整的I-Am响应消息
	response := []byte{
		// BVLC 头部
		BVLCTypeOriginalUnicast,                                      // BVLC类型：原始单播
		BVLCOriginalUnicastNPDU,                                      // BVLC功能：原始单播NPDU
		byte((totalLength - 4) >> 8), byte((totalLength - 4) & 0xFF), // 长度（不包括BVLC头部的4字节）

		// NPDU 头部
		NPDUVersion1,           // NPDU版本
		NPDUControlUnsegmented, // 控制字节：未分段
		0x00, 0x00,             // 目标网络号（未指定）
		0x00,       // 目标MAC地址长度（未指定）
		0x00, 0x00, // 源网络号（未指定）
		0x00, // 源MAC地址长度（未指定）
		0xFF, // 跳数

		// APDU 头部
		APDUTypeUnconfirmedService, // APDU类型：未确认服务
		byte(totalLength - 11),     // APDU长度（不包括APDU头部和服务选择器）
		0x00,                       // 保留字节

		// I-Am服务数据
		BACnetServiceUnconfirmedIAm, // 服务选择器：I-Am

		// 对象标识符编码 (Device类型 = 8, 2字节类型 + 4字节实例)
		0x0c, 0x00, // 标签类型8，长度4字节
		byte(model.ObjectTypeDevice << 4), // 高4位：对象类型(Device=8)
		0x00,                              // 低4位：保留 + 实例号高20位的前4位
		byte(deviceID >> 16),              // 实例号高20位的中8位
		byte(deviceID >> 8),               // 实例号高20位的低8位
		byte(deviceID & 0xFF),             // 实例号低8位

		// 最大APDU长度接受值
		0x21, 0x04, 0x00, MaxAPDUSize1024Bytes, // 最大APDU：1024字节

		// 分段支持能力
		0x24, 0x01, SegmentationNo, // 不支持分段

		// 厂商ID
		0x25, 0x02, 0x00, VendorIDDefault, // 厂商ID：默认值
	}

	fmt.Printf("创建I-Am响应：设备ID=%d, 设备类型=%d\n", deviceID, deviceObjID.Type)

	return response
}
