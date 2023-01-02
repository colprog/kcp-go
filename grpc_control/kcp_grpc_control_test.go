package grpc_control

import (
	context "context"
	"testing"

	"github.com/stretchr/testify/assert"
	grpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func TestPb(t *testing.T) {
	testGetConn := &GetSessionsRequest{}
	dataGetConn, err := proto.Marshal(testGetConn)
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}

	newGetConn := &GetSessionsRequest{}
	err = proto.Unmarshal(dataGetConn, newGetConn)
	if err != nil {
		t.Fatal("unmarshaling error: ", err)
	}
}

// Need server started
func TestGetSessions(t *testing.T) {
	conn, err := grpc.Dial(":2000", grpc.WithInsecure())
	assert.NoError(t, err)
	defer conn.Close()

	KCPSessionCtlCli := NewKCPSessionCtlClient(conn)
	getSessionsReply, err := KCPSessionCtlCli.GetSessions(context.Background(), &GetSessionsRequest{})

	assert.NoError(t, err)
	assert.Equal(t, len(getSessionsReply.Connections), 1)
}
