package id

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

var ErrRedisUnmarshal = errors.New("unsupported data")

type ChatID int64

type Key struct {
	UserName string
	ChatID   ChatID
}

func (id ChatID) String() string {
	return fmt.Sprintf("chat:%d", id)
}

// Implements encoding.BinaryMarshaler.
func (id ChatID) MarshalBinary() ([]byte, error) {
	return []byte(id.String()), nil
}

// Implements encoding.BinaryUnmarshaler.
func (id *ChatID) UnmarshalBinary(data []byte) error {
	const (
		fieldsNum = 1
		kvNum     = 2
	)

	values := strings.Split(string(data), ";")
	if len(values) != fieldsNum {
		return UnsupportedData(data)
	}

	chatKV := strings.Split(values[0], ":")
	if len(chatKV) != kvNum || chatKV[0] != "chat" {
		return UnsupportedData(data)
	}

	chatID, err := strconv.ParseInt(chatKV[1], 10, 64)
	if err != nil {
		return UnsupportedData(data)
	}

	*id = ChatID(chatID)

	return nil
}

func (key Key) String() string {
	return fmt.Sprintf("usr:%s;chat:%d", key.UserName, key.ChatID)
}

// Implements encoding.BinaryMarshaler.
func (key Key) MarshalBinary() ([]byte, error) {
	return []byte(key.String()), nil
}

// Implements encoding.BinaryUnmarshaler.
func (key *Key) UnmarshalBinary(data []byte) error {
	const (
		fieldsNum = 2
		kvNum     = 2
	)

	values := strings.Split(string(data), ";")
	if len(values) != fieldsNum {
		return UnsupportedData(data)
	}

	usrKV := strings.Split(values[0], ":")
	if len(usrKV) != kvNum || usrKV[0] != "usr" {
		return UnsupportedData(data)
	}

	var chatID ChatID
	if err := chatID.UnmarshalBinary([]byte(values[1])); err != nil {
		return err
	}

	key.UserName = usrKV[1]
	key.ChatID = chatID

	return nil
}

func UnsupportedData(data []byte) error {
	return errors.WithMessagef(ErrRedisUnmarshal, "%v unsupported", data)
}
