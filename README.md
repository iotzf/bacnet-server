# Go BACnet Server Simulator

这是一个使用Go语言实现的BACnet协议服务端模拟工具，可以模拟BACnet设备并响应基本的BACnet请求。

## 功能特性

- 支持BACnet/IP协议
- 响应基本的Who-Is请求并发送I-Am响应
- 可配置的设备信息
- 内置多种BACnet对象类型：
  - 模拟输入 (温度传感器、湿度传感器)
  - 二进制输出 (灯光控制、空调控制)
  - 模拟值 (温度设定点)

## 项目结构

```
├── cmd/
│   └── tool/           # 主应用程序入口
├── internal/
│   ├── model/          # BACnet对象模型
│   └── protocol/       # BACnet协议实现
├── go.mod              # Go模块定义
├── README.md           # 项目说明
└── Makefile            # 构建脚本
```

## 安装和运行

### 前置要求

- Go 1.16或更高版本

### 安装步骤

1. 克隆或下载本项目
2. 进入项目目录

```bash
cd bacnet-server
```

3. 构建应用程序

```bash
make build
```

4. 运行应用程序

```bash
make run
```

### 命令行参数

```
-port       BACnet服务监听端口，默认47808
-device-id  设备实例号，默认1001
-device-name 设备名称，默认"Go BACnet Server"
-location   设备物理位置，默认"Test Location"
```

## 示例用法

### 默认配置运行

```bash
./bacnet-tool
```

### 自定义配置运行

```bash
./bacnet-tool -port 47809 -device-id 2001 -device-name "My BACnet Device" -location "Building A, Floor 1"
```

## 注意事项

- 这是一个简化版的BACnet协议实现，主要用于学习和测试目的
- 当前实现支持的功能有限，仅响应基本的Who-Is请求
- 实际生产环境中，建议使用成熟的BACnet协议栈

## 开发说明

### 添加新的对象类型

在`internal/model/objects.go`中添加新的对象类型定义和实现。

### 扩展协议功能

在`internal/protocol/server.go`中实现更多的BACnet服务和消息处理逻辑。


## BACnetObject和Device区别

在BACnet协议实现中，BACnetObject和Device之间存在明确的区别和关系：

1. BACnetObject ：
   
   - 是实现BACnet对象的基础结构体，直接实现了Object接口
   - 包含通用的对象属性：标识符(Identifier)、名称(Name)、属性映射(Properties)、事件列表(Events)和订阅列表(Subscriptions)
   - 提供基础功能：获取对象标识符、名称、类型，以及读写属性等
   - 是所有BACnet对象的通用实现基础

2. Device ：
   
   - 通过嵌入(embedding)BACnetObject结构体继承其所有特性
   - 额外添加了Objects字段([]Object类型)，用于存储设备所包含的其他BACnet对象
   - 提供对象管理功能：AddObject()方法用于添加对象，FindObject()方法用于查找对象
   - 在创建时(NewDevice)会设置设备特有的默认属性，如位置、设备类型、制造商名称等

   
3. 关系 ：
   
   - Device是一种特殊的BACnet对象，它既是对象又作为容器管理其他对象
   - 继承关系：Device → BACnetObject → Object接口
   - 在BACnet协议架构中，Device代表整个BACnet设备，而BACnetObject是设备内各种功能对象(如传感器、控制器)的通用实现
总结来说，BACnetObject是所有BACnet对象的基础实现，而Device则是对BACnetObject的扩展，专门用于表示和管理完整的BACnet设备及其包含的所有对象。


## 建模要求

BACnet协议的**常见设计模式**，通常一个**物理设备实例**对应一个BACnet Device对象。 其他对象(如传感器、执行器等)通常作为Device的子对象存在。

在server下支持多设备实例，暂时不支持。例如：将device字段改为设备数组，修改相关方法实现，并调整消息处理逻辑以支持多设备场景。


## 属性读取流程

一个完整的ReadProperty请求处理应该包括以下步骤：

1. 接收ReadProperty请求消息
2. 解析请求参数，包括对象标识符、属性标识符、属性索引(如果有)等
3. 验证请求参数的有效性，如检查对象是否存在、属性是否可读写等
4. 如果请求的是单个属性值，直接返回该属性值
5. 如果请求的是属性数组，根据索引范围返回对应的值
6. 发送ReadProperty响应消息，包含请求的属性值或错误码

### 错误处理机制

- 对象不存在 → Object Error (Class 0x02, Code 0x01)
- 属性不存在 → Property Error (Class 0x03, Code 0x02)
- 属性不可写 → Property Error (Class 0x03, Code 0x04)
- 数据格式错误 → Service Error (Class 0x04, Code 0x05)

### 响应格式

- 读操作返回 ComplexAck （0x04）类型响应，包含实际属性值
- 写操作返回 SimpleAck （0x02）类型确认响应
- 错误情况返回 Error （0x03）类型响应