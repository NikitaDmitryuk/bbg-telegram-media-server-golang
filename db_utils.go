package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

func initDB() {
	dbPath := filepath.Join(GlobalConfig.MoviePath, "movie.db")
	db, _ = sql.Open("sqlite3", dbPath)
	if _, err := os.Stat(dbPath); err != nil {
		createTables()
	}
}

func createTables() {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS Movie (
			ID INTEGER PRIMARY KEY AUTOINCREMENT,
			NAME TEXT NOT NULL,
			UPLOADED_FILE TEXT NOT NULL,
			TORRENT_FILE TEXT NOT NULL,
			DOWNLOADED_PERCENTAGE INTEGER NOT NULL DEFAULT 0 CHECK (DOWNLOADED_PERCENTAGE BETWEEN 0 AND 100),
			DOWNLOADED BOOLEAN NOT NULL DEFAULT 0 CHECK (DOWNLOADED IN (0, 1))
		);
		CREATE TABLE IF NOT EXISTS User (
			ID INTEGER PRIMARY KEY AUTOINCREMENT,
			NAME TEXT NOT NULL,
			CHAT_ID INTEGER NOT NULL
		);
	`)
	if err != nil {
		log.Fatal("Error creating tables:", err)
	}
}

func addMovie(name, uploadedFile, torrentFile string) {
	_, err := db.Exec(`
		INSERT INTO Movie (NAME, UPLOADED_FILE, TORRENT_FILE)
		VALUES (?, ?, ?)
	`, name, uploadedFile, torrentFile)
	if err != nil {
		log.Println("Error adding movie:", err)
	}
}

// Other functions (setLoaded, getMovieByID, removeMovie, getMovieList, login, updateDownloadedPercentage, checkUser) go here...

func removeMovie(movieID int) {
	stmt, err := db.Prepare("DELETE FROM Movie WHERE ID = ?")
	if err != nil {
		panic(err)
	}
	_, err = stmt.Exec(movieID)
	if err != nil {
		panic(err)
	}
}

func getMovieList() []Movie {
	rows, err := db.Query("SELECT ID, NAME, DOWNLOADED, DOWNLOADED_PERCENTAGE FROM Movie ORDER BY ID")
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	var movies []Movie
	for rows.Next() {
		var movie Movie
		err := rows.Scan(&movie.ID, &movie.Name, &movie.Downloaded, &movie.DownloadedPercentage)
		if err != nil {
			panic(err)
		}
		movies = append(movies, movie)
	}
	return movies
}

func login(password string, chatID int64, userName string) bool {
	if password == GlobalConfig.Password {
		stmt, err := db.Prepare("INSERT INTO User (NAME, CHAT_ID) VALUES (?, ?)")
		if err != nil {
			panic(err)
		}
		_, err = stmt.Exec(userName, chatID)
		if err != nil {
			panic(err)
		}
		return true
	}
	return false
}

func updateDownloadedPercentage(name string, percentage int64) {
	stmt, err := db.Prepare("UPDATE Movie SET DOWNLOADED_PERCENTAGE = ? WHERE NAME = ?")
	if err != nil {
		panic(err)
	}
	_, err = stmt.Exec(percentage, name)
	if err != nil {
		panic(err)
	}
}

func checkUser(chatID int64) bool {
	stmt, err := db.Prepare("SELECT * FROM User WHERE CHAT_ID = ?")
	if err != nil {
		panic(err)
	}
	rows, err := stmt.Query(chatID)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	return rows.Next()
}
