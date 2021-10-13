package accountutils

import (
	crand "crypto/rand"
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/gogo/protobuf/proto"
	"github.com/ipfs/go-datastore"
	sync_ds "github.com/ipfs/go-datastore/sync"
	sqlds "github.com/ipfs/go-ds-sql"
	pgqueries "github.com/ipfs/go-ds-sql/postgres"
	"go.uber.org/zap"
	"golang.org/x/crypto/nacl/box"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"moul.io/zapgorm2"

	"berty.tech/berty/v2/go/internal/cryptoutil"
	"berty.tech/berty/v2/go/pkg/accounttypes"
	"berty.tech/berty/v2/go/pkg/errcode"
)

const (
	InMemoryDir               = ":memory:"
	DefaultPushKeyFilename    = "push.key"
	AccountMetafileName       = "account_meta"
	AccountNetConfFileName    = "account_net_conf"
	MessengerDatabaseFilename = "messenger.sqlite"
)

func GetDevicePushKeyForPath(filePath string, createIfMissing bool) (pk *[cryptoutil.KeySize]byte, sk *[cryptoutil.KeySize]byte, err error) {
	contents, err := ioutil.ReadFile(filePath)
	if os.IsNotExist(err) && createIfMissing {
		if err := os.MkdirAll(path.Dir(filePath), 0o700); err != nil {
			return nil, nil, errcode.ErrInternal.Wrap(err)
		}

		pk, sk, err = box.GenerateKey(crand.Reader)
		if err != nil {
			return nil, nil, errcode.ErrCryptoKeyGeneration.Wrap(err)
		}

		contents = make([]byte, cryptoutil.KeySize*2)
		for i := 0; i < cryptoutil.KeySize; i++ {
			contents[i] = pk[i]
			contents[i+cryptoutil.KeySize] = sk[i]
		}

		if _, err := os.Create(filePath); err != nil {
			return nil, nil, errcode.ErrInternal.Wrap(err)
		}

		if err := ioutil.WriteFile(filePath, contents, 0o600); err != nil {
			return nil, nil, errcode.ErrInternal.Wrap(err)
		}

		return pk, sk, nil
	} else if err != nil {
		return nil, nil, errcode.ErrPushUnableToDecrypt.Wrap(fmt.Errorf("unable to get device push key"))
	}

	pkVal := [cryptoutil.KeySize]byte{}
	skVal := [cryptoutil.KeySize]byte{}

	for i := 0; i < cryptoutil.KeySize; i++ {
		pkVal[i] = contents[i]
		skVal[i] = contents[i+cryptoutil.KeySize]
	}

	return &pkVal, &skVal, nil
}

func ListAccounts(rootDir string, logger *zap.Logger) ([]*accounttypes.AccountMetadata, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	if _, err := os.Stat(rootDir); os.IsNotExist(err) {
		return []*accounttypes.AccountMetadata{}, nil
	} else if err != nil {
		return nil, errcode.ErrBertyAccountFSError.Wrap(err)
	}

	subitems, err := ioutil.ReadDir(rootDir)
	if err != nil {
		return nil, errcode.ErrBertyAccountFSError.Wrap(err)
	}

	var accounts []*accounttypes.AccountMetadata

	for _, subitem := range subitems {
		if !subitem.IsDir() {
			continue
		}

		account, err := GetAccountMetaForName(rootDir, subitem.Name(), logger)
		if err != nil {
			accounts = append(accounts, &accounttypes.AccountMetadata{Error: err.Error(), AccountID: subitem.Name()})
		} else {
			accounts = append(accounts, account)
		}
	}

	return accounts, nil
}

func GetAccountMetaForName(rootDir string, accountID string, logger *zap.Logger) (*accounttypes.AccountMetadata, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	metafileName := filepath.Join(rootDir, accountID, AccountMetafileName)

	metaBytes, err := ioutil.ReadFile(metafileName)
	if os.IsNotExist(err) {
		return nil, errcode.ErrBertyAccountDataNotFound
	} else if err != nil {
		logger.Warn("unable to read account metadata", zap.Error(err), zap.String("account-id", accountID))
		return nil, errcode.ErrBertyAccountFSError.Wrap(fmt.Errorf("unable to read account metadata: %w", err))
	}

	meta := &accounttypes.AccountMetadata{}
	if err := proto.Unmarshal(metaBytes, meta); err != nil {
		return nil, errcode.ErrDeserialization.Wrap(fmt.Errorf("unable to unmarshall account metadata: %w", err))
	}

	meta.AccountID = accountID

	return meta, nil
}

func GetDatastoreDir(dir string) (string, error) {
	switch {
	case dir == "":
		return "", errcode.TODO.Wrap(fmt.Errorf("--store.dir is empty"))
	case dir == InMemoryDir:
		return InMemoryDir, nil
	}

	dir = path.Join(dir, "account0") // account0 is a suffix that will be used with multi-account later

	_, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", errcode.TODO.Wrap(err)
		}
	case err != nil:
		return "", errcode.TODO.Wrap(err)
	}

	return dir, nil
}

func GetRootDatastoreForPath(dir string, logger *zap.Logger) (datastore.Batching, error) {
	inMemory := dir == InMemoryDir

	var ds datastore.Batching
	if inMemory {
		ds = datastore.NewMapDatastore()
	} else {
		const tableName = "blocks"

		// Open database
		// key := "DEADBEEF51E7B56E4697B0E1F08507293D761A05CE4D1B628663F411A8086D99"
		dbPath := filepath.Join(dir, "datastore.sqlite")
		// dbURL := fmt.Sprintf("%s?_pragma_key=x'%s'&_pragma_cipher_page_size=4096", dbPath, key)
		dbURL := dbPath
		db, err := sql.Open("sqlite3", dbURL)
		if err != nil {
			return nil, errcode.ErrDBOpen.Wrap(err)
		}

		// Create table if not exists
		if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			key TEXT PRIMARY KEY,
			data BLOB
		) WITHOUT ROWID;`, tableName)); err != nil {
			return nil, errcode.ErrDBWrite.Wrap(err)
		}

		// Implement the Queries interface for your SQL impl.
		// ...or use the provided PostgreSQL queries
		// pg queries seem to work for sqlite
		queries := pgqueries.NewQueries(tableName)

		// Instantiate ds
		ds = sqlds.NewDatastore(db, queries)
	}

	ds = sync_ds.MutexWrap(ds)

	return ds, nil
}

func GetMessengerDBForPath(dir string, logger *zap.Logger) (*gorm.DB, func(), error) {
	var sqliteConn string
	if dir == InMemoryDir {
		sqliteConn = ":memory:"
	} else {
		sqliteConn = path.Join(dir, MessengerDatabaseFilename)
	}

	cfg := &gorm.Config{
		Logger:                                   zapgorm2.New(logger.Named("gorm")),
		DisableForeignKeyConstraintWhenMigrating: true,
	}
	db, err := gorm.Open(sqlite.Open(sqliteConn), cfg)
	if err != nil {
		return nil, nil, errcode.TODO.Wrap(err)
	}

	return db, func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}, nil
}