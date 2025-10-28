package protocol

import (
	"net"
	"reflect"
	"testing"

	"github.com/iotzf/bacnet-server/internal/model"
)

func TestBACnetServer_processBACnetMessage(t *testing.T) {
	type fields struct {
		device            *model.Device
		udpConn           *net.UDPConn
		localAddr         *net.UDPAddr
		Running           bool
		currentClientAddr string
	}
	type args struct {
		data []byte
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []byte
		wantErr bool
	}{
		// TODO: Add test cases.
		{
			name: "who is 81 0b 00 08 01 00 10 08",
			fields: fields{
				device:            nil,
				udpConn:           nil,
				localAddr:         nil,
				Running:           false,
				currentClientAddr: "",
			},
			args: args{
				data: []byte{0x81, 0x0b, 0x00, 0x08, 0x01, 0x00, 0x10, 0x08},
			},
			want:    []byte{},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &BACnetServer{
				device:            tt.fields.device,
				udpConn:           tt.fields.udpConn,
				localAddr:         tt.fields.localAddr,
				Running:           tt.fields.Running,
				currentClientAddr: tt.fields.currentClientAddr,
			}
			got, err := s.processBACnetMessage(tt.args.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("processBACnetMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("processBACnetMessage() got = %v, want %v", got, tt.want)
			}
		})
	}
}
