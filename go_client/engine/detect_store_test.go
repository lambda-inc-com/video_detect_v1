package engine

import (
	"context"
	"fmt"
	"go_client/config"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	_config, err := config.BindConfig([]string{})
	if err != nil {
		fmt.Println(err)
		return
	}
	v1, err := NewStoreV1(_config)
	if err != nil {
		fmt.Println(err)
	}
	err = v1.StoreRecordVideo(context.Background(), StoreRecordVideoDto{
		PathURL:        "test",
		StartTimestamp: time.Now().Local().Add(-time.Hour * 1).Unix(),
		EndTimestamp:   time.Now().Local().Unix(),
		ID:             "testtttaaa",
	})
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("发送订阅成功..")
}
