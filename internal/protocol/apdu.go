package protocol

import (
	"errors"
	"fmt"
	"strings"
)

// APDU类型常量  常见 PDU 类型编码（高 4 位）
const (
	BACnetAPDUTypeConfirmedServiceRequest   = 0x0
	BACnetAPDUTypeUnconfirmedServiceRequest = 0x1
	BACnetAPDUTypeSimpleAck                 = 0x2
	BACnetAPDUTypeComplexAck                = 0x3
	BACnetAPDUTypeSegmentAck                = 0x4
	BACnetAPDUTypeError                     = 0x5
	BACnetAPDUTypeReject                    = 0x6
	BACnetAPDUTypeAbort                     = 0x7
)

// BACnet服务类型常量
const (
	BACnetServiceUnconfirmedWhoIs               = 0x08
	BACnetServiceConfirmedReadProperty          = 0x0c
	BACnetServiceConfirmedWriteProperty         = 0x0d
	BACnetServiceConfirmedReadPropertyMultiple  = 0x10
	BACnetServiceConfirmedWritePropertyMultiple = 0x11
	BACnetServiceConfirmedAcknowledgeAlarm      = 0x0f
	BACnetServiceUnconfirmedEventNotification   = 0x09
	BACnetServiceConfirmedAtomicReadFile        = 0x14
	BACnetServiceConfirmedAtomicWriteFile       = 0x15
	BACnetServiceConfirmedDeleteFile            = 0x16
	BACnetServiceConfirmedSubscribeCOV          = 0x0e
	BACnetServiceConfirmedSubscribeCOVProperty  = 0x48
	BACnetServiceConfirmedCancelCOVSubscription = 0x25
)

// APDU 表示解析后的 APDU 内容（尽量包含常用字段）
type APDU struct {
	PDUType            byte   // 高4位 PDU 类型（原始值）
	ControlFlags       byte   // 低4位控制标志（原始字节 & 0x0F）
	InvokeID           *byte  // 可选（仅存在于某些 PDU）
	ServiceChoice      *byte  // 可选：服务选择器（存在于大多数服务相关 PDU）
	SequenceNumber     *byte  // 可选（分段场景）
	ProposedWindowSize *byte  // 可选（分段场景）
	Payload            []byte // 剩余服务参数 / 有效载荷
	Raw                []byte // 原始 APDU 数据副本
}

// ParseAPDU 解析传入的 APDU 字节，返回结构化信息。
// 解析遵循常见 BACnet APDU 帧格式的约定：
// - Confirmed service request: octet0(type/flags), octet1(maxSegs/maxApdu), octet2(invokeID), octet3(serviceChoice), octet4..payload
// - Unconfirmed service: octet0(type/flags), octet1(serviceChoice), octet2..payload
// - SimpleAck: octet0(type/flags), octet1(reserved), octet2(invokeID), octet3(serviceChoice), octet4..payload
// - ComplexAck: octet0(type/flags), octet1(reserved), octet2(invokeID), octet3(length), octet4(serviceChoice), octet5..payload
// - Error: octet0(type/flags), octet1(reserved), octet2(invokeID), octet3(length), octet4(serviceChoice), octet5..error data
// 解析器对长度做防护，遇到无法识别的格式会返回错误。
func ParseAPDU(data []byte) (*APDU, error) {
	if len(data) < 1 {
		return nil, errors.New("empty APDU")
	}

	raw := make([]byte, len(data))
	copy(raw, data)

	first := data[0]
	pduType := first >> 4
	control := first & 0x0F

	result := &APDU{
		PDUType:      pduType,
		ControlFlags: control,
		Raw:          raw,
	}

	switch pduType {
	case BACnetAPDUTypeConfirmedServiceRequest:
		// 需要至少 4 字节 (octet0, octet1, invokeID, serviceChoice)
		if len(data) < 4 {
			return nil, fmt.Errorf("confirmed service request too short: %d", len(data))
		}
		invoke := data[2]
		sc := data[3]
		result.InvokeID = &invoke
		result.ServiceChoice = &sc
		if len(data) > 4 {
			result.Payload = data[4:]
		} else {
			result.Payload = nil
		}
		return result, nil

	case BACnetAPDUTypeUnconfirmedServiceRequest:
		// 需要至少 2 字节 (octet0, serviceChoice)
		if len(data) < 2 {
			return nil, fmt.Errorf("unconfirmed service too short: %d", len(data))
		}
		sc := data[1]
		result.ServiceChoice = &sc
		if len(data) > 2 {
			result.Payload = data[2:]
		} else {
			result.Payload = nil
		}
		return result, nil

	case BACnetAPDUTypeSimpleAck:
		// 常见格式：octet0, octet1(reserved), octet2(invokeID), octet3(serviceChoice), rest payload(optional)
		if len(data) < 4 {
			return nil, fmt.Errorf("simple ack too short: %d", len(data))
		}
		invoke := data[2]
		sc := data[3]
		result.InvokeID = &invoke
		result.ServiceChoice = &sc
		if len(data) > 4 {
			result.Payload = data[4:]
		}
		return result, nil

	case BACnetAPDUTypeComplexAck:
		// 常见格式：octet0, octet1, octet2(invokeID), octet3(len), octet4(serviceChoice), octet5..payload
		if len(data) < 5 {
			return nil, fmt.Errorf("complex ack too short: %d", len(data))
		}
		invoke := data[2]
		// lengthByte := data[3] // 有时表示后续长度
		sc := data[4]
		result.InvokeID = &invoke
		result.ServiceChoice = &sc
		if len(data) > 5 {
			result.Payload = data[5:]
		}
		return result, nil

	case BACnetAPDUTypeError:
		// 常见格式：octet0, octet1, octet2(invokeID), octet3(len), octet4(serviceChoice), octet5..error data
		if len(data) < 5 {
			return nil, fmt.Errorf("error PDU too short: %d", len(data))
		}
		invoke := data[2]
		sc := data[4]
		result.InvokeID = &invoke
		result.ServiceChoice = &sc
		if len(data) > 5 {
			result.Payload = data[5:]
		}
		return result, nil

	default:
		// 未知或未实现的 PDU 类型，填充原始负载返回给调用者进一步处理
		if len(data) > 1 {
			result.Payload = data[1:]
		} else {
			result.Payload = nil
		}
		return result, nil
	}
}

// pduTypeName 返回 PDU 类型可读名称
func pduTypeName(t byte) string {
	switch t {
	case BACnetAPDUTypeConfirmedServiceRequest:
		return "ConfirmedServiceRequest"
	case BACnetAPDUTypeUnconfirmedServiceRequest:
		return "UnconfirmedServiceRequest"
	case BACnetAPDUTypeSimpleAck:
		return "SimpleAck"
	case BACnetAPDUTypeComplexAck:
		return "ComplexAck"
	case BACnetAPDUTypeSegmentAck:
		return "SegmentAck"
	case BACnetAPDUTypeError:
		return "Error"
	case BACnetAPDUTypeReject:
		return "Reject"
	case BACnetAPDUTypeAbort:
		return "Abort"
	default:
		return "Unknown"
	}
}

// String 返回 APDU 的可读字符串表示，便于调试
func (a *APDU) String() string {
	if a == nil {
		return "<nil APDU>"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "APDU{Type:%s(0x%02x), Flags:0x%02x", pduTypeName(a.PDUType), a.PDUType, a.ControlFlags)

	if a.InvokeID != nil {
		fmt.Fprintf(&sb, ", InvokeID:%d", *a.InvokeID)
	}
	if a.ServiceChoice != nil {
		fmt.Fprintf(&sb, ", ServiceChoice:0x%02x", *a.ServiceChoice)
	}
	if a.SequenceNumber != nil {
		fmt.Fprintf(&sb, ", Sequence:%d", *a.SequenceNumber)
	}
	if a.ProposedWindowSize != nil {
		fmt.Fprintf(&sb, ", Window:%d", *a.ProposedWindowSize)
	}

	if len(a.Payload) > 0 {
		fmt.Fprintf(&sb, ", PayloadLen:%d", len(a.Payload))
		// 显示前最多16字节的 payload 前缀，超过则以 ... 结尾
		prefixLen := 16
		if len(a.Payload) < prefixLen {
			prefixLen = len(a.Payload)
		}
		fmt.Fprintf(&sb, ", PayloadPrefix:% x", a.Payload[:prefixLen])
		if len(a.Payload) > prefixLen {
			sb.WriteString("...")
		}
	}

	sb.WriteString("}")
	return sb.String()
}
