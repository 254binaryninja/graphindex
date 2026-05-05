package db

import (
	"database/sql"
	"runtime"

	_ "modernc.org/sqlite"
)

type DB struct {
	Write *sql.DB
	Read  *sql.DB
}

func Open(path string) (*DB, error) {
	writeDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	writeDB.SetMaxOpenConns(1)

	readDB, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		writeDB.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(runtime.NumCPU())

	if err := Migrate(writeDB); err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, err
	}

	return &DB{Write: writeDB, Read: readDB}, nil
}

func (d *DB) Close() error {
	d.Read.Close()
	return d.Write.Close()
}
