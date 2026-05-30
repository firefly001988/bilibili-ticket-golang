package configuration

import (
	"bilibili-ticket-golang/store/cookiejar"
	"os"
	"path/filepath"

	"github.com/ugorji/go/codec"
)

// Data stroage, user should not edit it
// use binary file to store data, so it is not easy to be modified by user

const dataFileName = "data/store.bin"

type DataStorage struct {
	Cookies         []cookiejar.CookieEntries `json:"cookies"`
	TicketData      *TicketData               `json:"ticketData"`
	BWSData         *BWSData                  `json:"bwsData"`
	NotifyChData    *NotifyChannelData        `json:"notifyChannelData"`
	RefreshToken    string                    `json:"refreshToken"`
	RetryIntervalMs int                       `json:"retryIntervalMs"`
	StartDelayMs    int                       `json:"startDelayMs"`
}

func NewDataStorage() *DataStorage {
	return &DataStorage{
		Cookies:         []cookiejar.CookieEntries{},
		TicketData:      NewTicketData(),
		BWSData:         NewBWSData(),
		NotifyChData:    NewNotifyChannelData(),
		RetryIntervalMs: 500, // default 500ms
		StartDelayMs:    50,  // default 50ms
	}
}

func (d *DataStorage) Load() error {
	return d.readFromFile(dataFileName)
}

func (d *DataStorage) Save() error {
	return d.writeToFile(dataFileName)
}

func (d *DataStorage) readFromFile(filename string) error {
	if err := ensureParentDir(filename); err != nil {
		return err
	}

	file, err := os.OpenFile(filename, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			// Initialize missing storage file with default content.
			return d.writeToFile(filename)
		}
		return err
	}
	defer file.Close()
	decoder := codec.NewDecoder(file, &codec.MsgpackHandle{})
	err = decoder.Decode(d)
	if err != nil {
		return err
	}
	return nil
}

func (d *DataStorage) writeToFile(filename string) error {
	if err := ensureParentDir(filename); err != nil {
		return err
	}

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := codec.NewEncoder(file, &codec.MsgpackHandle{})
	err = encoder.Encode(d)
	if err != nil {
		return err
	}
	return nil
}

func ensureParentDir(filename string) error {
	dir := filepath.Dir(filename)
	if dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
