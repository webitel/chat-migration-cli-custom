package olddb

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is a connection to the legacy monolithic chat service database.
type DB struct {
	pool *pgxpool.Pool

	appStore          *AppStore
	botStore          *BotStore
	clientStore       *ClientStore
	conversationStore *ConversationStore
	messageStore      *MessageStore
}

func New(pool *pgxpool.Pool, _ bool) (*DB, error) {
	db := &DB{pool: pool}

	return db, nil
}

func (db *DB) Pool() *pgxpool.Pool { return db.pool }

func (db *DB) Close() { db.pool.Close() }

func (db *DB) AppStore() *AppStore {
	if db.appStore == nil {
		db.appStore = NewAppStore(db)
	}

	return db.appStore
}

func (db *DB) BotStore() *BotStore {
	if db.botStore == nil {
		db.botStore = NewBotStore(db)
	}

	return db.botStore
}

func (db *DB) ClientStore() *ClientStore {
	if db.clientStore == nil {
		db.clientStore = NewClientStore(db)
	}

	return db.clientStore
}

func (db *DB) ConversationStore() *ConversationStore {
	if db.conversationStore == nil {
		db.conversationStore = NewConversationStore(db)
	}

	return db.conversationStore
}

func (db *DB) MessageStore() *MessageStore {
	if db.messageStore == nil {
		db.messageStore = NewMessageStore(db)
	}

	return db.messageStore
}
