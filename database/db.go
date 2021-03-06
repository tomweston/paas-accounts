package database

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate"
	"github.com/golang-migrate/migrate/database/postgres"
	_ "github.com/golang-migrate/migrate/source/file"
	"github.com/lib/pq"
)

type User struct {
	UUID string
}

type Document struct {
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	ValidFrom time.Time `json:"valid_from"`
}

type Agreement struct {
	UserUUID     string    `json:"user_uuid"`
	DocumentName string    `json:"document_name"`
	Date         time.Time `json:"date"`
}

type UserDocument struct {
	Name          string     `json:"name"`
	Content       string     `json:"content"`
	ValidFrom     time.Time  `json:"valid_from"`
	AgreementDate *time.Time `json:"agreement_date"`
}

var (
	ErrDocumentNotFound = errors.New("document not found")
	ErrUserNotFound     = errors.New("user not found")
)

func sqlDir() string {
	root := os.Getenv("APP_ROOT")
	if root == "" {
		root = os.Getenv("PWD")
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	return filepath.Join(root, "database", "sql")
}

type DB struct {
	conn *sql.DB
}

func NewDB(connstr string) (*DB, error) {
	conn, err := sql.Open("postgres", connstr)
	if err != nil {
		return nil, err
	}

	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) Init() error {
	driver, err := postgres.WithInstance(db.conn, &postgres.Config{})
	m, err := migrate.NewWithDatabaseInstance("file://"+sqlDir(), "postgres", driver)
	if err != nil {
		return err
	}

	defer m.Close()
	if err := m.Up(); err != migrate.ErrNoChange {
		return err
	}

	return nil
}

func (db *DB) PutDocument(doc Document) error {
	latestDocVersion, err := db.GetDocument(doc.Name)
	if err != nil && err != ErrDocumentNotFound {
		return err
	}

	if err == ErrDocumentNotFound || latestDocVersion.Content != doc.Content {
		_, err = db.conn.Exec(`INSERT INTO documents (name, content, valid_from) VALUES ($1, $2, $3)`, doc.Name, doc.Content, doc.ValidFrom)
		return err
	}

	return nil
}

func (db *DB) GetDocument(name string) (Document, error) {
	doc := Document{}
	err := db.conn.QueryRow(`SELECT name, content, valid_from FROM documents WHERE name = $1 ORDER BY valid_from DESC LIMIT 1`, name).Scan(&doc.Name, &doc.Content, &doc.ValidFrom)

	if err == sql.ErrNoRows {
		err = ErrDocumentNotFound
	}

	return doc, err
}

func (db *DB) PutUser(user User) error {
	_, err := db.conn.Exec(`INSERT INTO users (uuid) VALUES ($1) ON CONFLICT DO NOTHING`, user.UUID)

	return err
}

func (db *DB) GetUser(uuid string) (User, error) {
	user := User{}
	err := db.conn.QueryRow(`
		SELECT uuid FROM users WHERE uuid = $1
	`, uuid).Scan(&user.UUID)

	if err == sql.ErrNoRows {
		err = ErrUserNotFound
	}

	return user, err
}

func (db *DB) PutAgreement(agreement Agreement) error {
	_, err := db.conn.Exec(`
		INSERT INTO agreements (
			user_uuid, document_name, date
		) VALUES (
			$1, $2, $3
		)
	`, agreement.UserUUID, agreement.DocumentName, agreement.Date)

	return err
}

func (db *DB) GetDocumentsForUserUUID(uuid string) ([]UserDocument, error) {
	rows, err := db.conn.Query(`
		WITH valid_documents AS (
			SELECT
				*,
				tstzrange(valid_from, lead(valid_from, 1, 'infinity') over (
						partition by name order by valid_from rows between current row and 1 following
				)) as valid_for
			FROM
				documents
		)
		SELECT
			d.name,
			d.content,
			d.valid_from,
			agreements.date
		FROM
			valid_documents d
		LEFT JOIN
			agreements ON (
				d.name = agreements.document_name
				AND agreements.date <@ d.valid_for
				AND agreements.user_uuid = $1
			)
		ORDER BY
			agreements.date
	`, uuid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	userDocuments := []UserDocument{}
	for rows.Next() {
		var userDocument UserDocument
		var nullTime pq.NullTime
		err := rows.Scan(&userDocument.Name, &userDocument.Content, &userDocument.ValidFrom, &nullTime)
		if err != nil {
			return nil, err
		}
		if nullTime.Valid {
			userDocument.AgreementDate = &nullTime.Time
		}
		userDocuments = append(userDocuments, userDocument)
	}
	return userDocuments, nil
}

func (db *DB) GetAgreementsForUserUUID(uuid string) ([]Agreement, error) {
	rows, err := db.conn.Query(`
		SELECT
			user_uuid, document_name, date
		FROM
			agreements
		WHERE
			user_uuid = $1
		ORDER BY
			date
	`, uuid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agreements := []Agreement{}
	for rows.Next() {
		var agreement Agreement
		err := rows.Scan(&agreement.UserUUID, &agreement.DocumentName, &agreement.Date)
		if err != nil {
			return nil, err
		}
		agreements = append(agreements, agreement)
	}

	return agreements, nil
}

func (db *DB) Ping() error {
	return db.conn.Ping()
}
