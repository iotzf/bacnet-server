package protocol

import (
	"encoding/binary"
	"fmt"
)

// NPDU 表示BACnet NPDU可选头部字段的解析结果
type NPDU struct {
	Version            byte
	Control            ControlInfo
	DestinationNetwork *uint16
	DestinationMAC     []byte
	SourceNetwork      *uint16
	SourceMAC          []byte
	HopCount           *byte
}

type ControlInfo struct {
	NetworkMessageFlag bool // bit 7 =1 表示为网络层消息（而非APDU）
	// reserved             bool         // bit 6 保留，必须为0
	DestinationSpecified bool // bit 5 =1 表示包含目标网络与MAC地址
	// reserved             bool         // bit 4 保留，必须为0
	SourceSpecified bool         // bit 3 =1 表示包含源网络与MAC地址
	ExpectingReply  bool         // bit 2 =1 表示期望回复
	Priority        PriorityInfo // bits 0-1 优先级
}

// ControlInfo string 返回控制信息的字符串表示
func (c ControlInfo) String() string {
	ret := ""
	if c.NetworkMessageFlag {
		ret += "Network Message, "
	} else {
		ret += "Apdu Message, "
	}

	if c.DestinationSpecified {
		ret += "Destination Specified, "
	} else {
		ret += "No Destination, "
	}

	if c.SourceSpecified {
		ret += "Source Specified, "
	} else {
		ret += "No Source, "
	}

	if c.ExpectingReply {
		ret += "Expecting Reply, "
	} else {
		ret += "No Reply Expected, "
	}

	ret += "Priority: " + c.Priority.String()

	return ret
}

type PriorityInfo byte

const (
	PriorityNormal     PriorityInfo = iota // 00
	PriorityUrgent                         // 01
	PriorityCritical                       // 10
	PriorityLifeSafety                     // 11
)

func (p PriorityInfo) String() string {
	switch p {
	case PriorityNormal:
		return "Normal"
	case PriorityUrgent:
		return "Urgent"
	case PriorityCritical:
		return "Critical"
	case PriorityLifeSafety:
		return "LifeSafety"
	default:
		return fmt.Sprintf("Priority(%d)", byte(p))
	}
}

func ParsePriority(data byte) PriorityInfo {
	return PriorityInfo(data & 0x03)
}

func ParseControl(data byte) ControlInfo {
	return ControlInfo{
		NetworkMessageFlag: (data & 0x80) != 0,
		//reserved:             false,
		DestinationSpecified: (data & 0x20) != 0,
		//reserved:             false,
		SourceSpecified: (data & 0x08) != 0,
		ExpectingReply:  (data & 0x04) != 0,
		Priority:        ParsePriority(data),
	}
}

// ParseNPDU 解析NPDU并返回解析到APDU开始处的偏移量
// 返回 NPDU 结构体、APDU 起始偏移和错误（若格式不符合或越界）
func ParseNPDU(data []byte) (NPDU, int, error) {
	var npdu NPDU

	// 最少需要版本和控制字节
	if len(data) < 2 {
		return npdu, 0, fmt.Errorf("NPDU too short")
	}

	npdu.Version = data[0]
	npdu.Control = ParseControl(data[1])
	if npdu.Version != 0x01 {
		return npdu, 0, fmt.Errorf("unsupported NPDU version: %02x", npdu.Version)
	}

	offset := 2

	// 如果设置了 destination specified (常见使用 0x20 位)，解析目标网络与目标MAC
	if npdu.Control.DestinationSpecified {
		// 需要至少 DNET(2) + DLEN(1)
		if offset+3 > len(data) {
			return npdu, 0, fmt.Errorf("NPDU too short for destination fields")
		}
		dnet := binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
		dlen := int(data[offset])
		offset++
		if offset+dlen > len(data) {
			return npdu, 0, fmt.Errorf("NPDU too short for destination MAC")
		}
		dmac := make([]byte, dlen)
		copy(dmac, data[offset:offset+dlen])
		offset += dlen

		npdu.DestinationNetwork = new(uint16)
		*npdu.DestinationNetwork = dnet
		npdu.DestinationMAC = dmac
	}

	// 若剩余足够，尝试解析源网络与源MAC（很多实现不强制存在，仅当足够数据时解析）
	if npdu.Control.SourceSpecified && len(data)-offset >= 3 {
		// SNET(2) + SLEN(1)
		snet := binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
		slen := int(data[offset])
		offset++
		if offset+slen > len(data) {
			return npdu, 0, fmt.Errorf("NPDU too short for source MAC")
		}
		smac := make([]byte, slen)
		copy(smac, data[offset:offset+slen])
		offset += slen

		npdu.SourceNetwork = new(uint16)
		*npdu.SourceNetwork = snet
		npdu.SourceMAC = smac
	}

	// 如果还有字节，通常是hop count(1)
	if npdu.Control.DestinationSpecified && offset < len(data) {
		h := data[offset]
		offset++
		npdu.HopCount = new(byte)
		*npdu.HopCount = h
	}

	if offset > len(data) {
		return npdu, 0, fmt.Errorf("NPDU parsing overflow")
	}

	return npdu, offset, nil
}

// Encode 将 NPDU 编码为字节序列（不包含BVLC头）
// 用于构造发送时的NPDU部分
func (n NPDU) Encode() []byte {
	out := []byte{n.Version}

	if n.DestinationNetwork != nil {
		out = append(out, byte((*n.DestinationNetwork)>>8), byte(*n.DestinationNetwork))
		if len(n.DestinationMAC) > 255 {
			// 截断到255，避免无效长度
			out = append(out, 255)
			out = append(out, n.DestinationMAC[:255]...)
		} else {
			out = append(out, byte(len(n.DestinationMAC)))
			out = append(out, n.DestinationMAC...)
		}
	}

	if n.SourceNetwork != nil {
		out = append(out, byte((*n.SourceNetwork)>>8), byte(*n.SourceNetwork))
		if len(n.SourceMAC) > 255 {
			out = append(out, 255)
			out = append(out, n.SourceMAC[:255]...)
		} else {
			out = append(out, byte(len(n.SourceMAC)))
			out = append(out, n.SourceMAC...)
		}
	}

	if n.HopCount != nil {
		out = append(out, *n.HopCount)
	}

	return out
}
