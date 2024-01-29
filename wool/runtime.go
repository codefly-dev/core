package wool

import "sync"

func IsDebug() bool {
	return globalLogLevel >= DEBUG
}

var lock *sync.Mutex

func init() {
	lock = &sync.Mutex{}
}

func GlobalLogLevel() Loglevel {
	lock.Lock()
	defer lock.Unlock()
	return globalLogLevel
}

func SetGlobalLogLevel(loglevel Loglevel) {
	lock.Lock()
	defer lock.Unlock()
	globalLogLevel = loglevel
}

var (
	globalLogLevel = INFO
)
