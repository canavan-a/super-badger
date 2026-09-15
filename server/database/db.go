package database

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"main/database/migrations"
)

func Connect(path string) (*gorm.DB, error) {
	// sqlite reports a missing parent directory as a misleading "out of
	// memory (14)" (14 is actually SQLITE_CANTOPEN) rather than a clear
	// not-found error, so ensure the directory exists up front.
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	// WAL + a busy timeout let concurrent HTTP requests queue briefly on a
	// write instead of immediately failing with SQLITE_BUSY/SQLITE_READONLY,
	// which gin's concurrent request handling can otherwise trigger even
	// with a single well-behaved process.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	// GetDataPointSetting/UpsertStationDataPoint etc. probe for a row that
	// often doesn't exist yet (e.g. a key with no per-station override) and
	// handle gorm.ErrRecordNotFound as a normal "no override" result — but
	// gorm's default logger still logs every such miss regardless, which
	// floods the logs under normal operation. Silence just that case.
	gormLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		},
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormLogger})
	if err != nil {
		return nil, err
	}

	// SQLite allows only one writer at a time regardless of WAL; capping the
	// pool at one connection serializes writes through Go's own queueing
	// instead of racing multiple connections against SQLite's own locking.
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)

	if err := migrations.Run(sqlDB); err != nil {
		return nil, err
	}

	return db, nil
}
