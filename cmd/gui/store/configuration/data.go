package configuration

import (
	"bilibili-ticket-golang/cmd/gui/store/cookiejar"
	"os"
	"path/filepath"
	"sync"

	"github.com/ugorji/go/codec"
)

// Data storage, user should not edit it
// use binary file to store data, so it is not easy to be modified by user

const dataFileName = "data/store.bin"

type DataStorage struct {
	Cookies         []cookiejar.CookieEntries `json:"cookies"`
	TicketData      *TicketData               `json:"ticketData"`
	BWSData         *BWSScheduler             `json:"bwsData"`
	NotifyChData    *NotifyChannelData        `json:"notifyChannelData"`
	RefreshToken    string                    `json:"refreshToken"`
	RetryIntervalMs int                       `json:"retryIntervalMs"`
	StartDelayMs    int                       `json:"startDelayMs"`
	Locale          string                    `json:"locale"`

	// ChainTrigger controls when the scheduler auto-starts the next ticket in
	// a buyer group's chain after the current task terminates.
	//   "success" (default): only start the next ticket when the current task
	//     succeeds (StatSuccess).
	//   "any": start the next ticket on any terminal state (success, failed,
	//     error).
	ChainTrigger string `json:"chainTrigger"`

	saveMu sync.Mutex
}

func NewDataStorage() *DataStorage {
	return &DataStorage{
		Cookies:         []cookiejar.CookieEntries{},
		TicketData:      NewTicketData(),
		BWSData:         NewBWSScheduler(),
		NotifyChData:    NewNotifyChannelData(),
		RetryIntervalMs: 500, // default 500ms
		StartDelayMs:    50,  // default 50ms
		ChainTrigger:    "success",
	}
}

func (d *DataStorage) Load() error {
	d.saveMu.Lock()
	defer d.saveMu.Unlock()
	return d.readFromFile(dataFileName)
}

func (d *DataStorage) Save() error {
	d.saveMu.Lock()
	defer d.saveMu.Unlock()
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

// writeToFile writes DataStorage to filename atomically: encodes to a
// temporary file, then renames it over the target. This prevents file
// corruption when Save is interrupted (crash / power loss) and ensures
// readers always see either the old file or the complete new file.
func (d *DataStorage) writeToFile(filename string) error {
	if err := ensureParentDir(filename); err != nil {
		return err
	}

	tmpFile := filename + ".tmp"
	file, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	encoder := codec.NewEncoder(file, &codec.MsgpackHandle{})
	encErr := encoder.Encode(d)
	closeErr := file.Close()
	// On encode error, clean up temp file and return.
	if encErr != nil {
		_ = os.Remove(tmpFile)
		return encErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpFile)
		return closeErr
	}
	// Atomic rename: replaces the target only after a successful write.
	return os.Rename(tmpFile, filename)
}

func ensureParentDir(filename string) error {
	dir := filepath.Dir(filename)
	if dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
