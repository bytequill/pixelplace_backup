package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB
var TOKEN string

const CooldownTime time.Duration = 10 * time.Minute

var Cooldowns map[int]CooldownData = make(map[int]CooldownData)

type Place struct {
	ID int `json:"id"`
}

type CooldownData struct {
	Pending  []byte
	LastIP   string
	NextTime time.Time
}

type LogData struct {
	ID        int       `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	ImageData []byte    `json:"image_data"`
	PlaceID   int       `json:"place_id"`
}

func InsertNewPlace(id int) {
	_, err := db.Exec("INSERT INTO places (id) VALUES (?)", id)
	if err != nil {
		fmt.Println(err)
		return
	}
}

func CheckForPlace(id int) bool {
	var result bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM places WHERE id = ?)", id).Scan(&result)
	if err != nil {
		if err == sql.ErrNoRows {
			return false
		}
		return false
	}
	return result
}

func AppendLog(data []byte, placeID int, ip string) {
	_, err := db.Exec("INSERT INTO log_data (image_data, place_id, req_ip) VALUES (?, ?, ?)", data, placeID, ip)
	if err != nil {
		fmt.Println(err)
		return
	}
}

func SubmitCooldown(id int) {
	c := Cooldowns[id]
	time.Sleep(time.Until(c.NextTime))

	if len(c.Pending) > 0 {
		log.Debug("Submitted a cooldown by", "ip", c.LastIP)
		AppendLog(c.Pending, id, c.LastIP)
	}
}

func main() {
	log.SetLevel(log.DebugLevel)
	TOKEN = "ILOVEKISSINGBOYS"
	//TOKEN = os.Getenv("TOKEN")
	if len(TOKEN) == 0 {
		panic("Please enter a valid token")
	}

	// Connect to the SQLite database
	var err error
	db, err = sql.Open("sqlite3", "./data.db")
	if err != nil {
		panic(err)
	}
	err = db.Ping()
	if err != nil {
		panic(err)
	}

	// Create the tables
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS places (
            id INTEGER PRIMARY KEY
        );
    `)
	if err != nil {
		fmt.Println(err)
		return
	}

	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS log_data (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            image_data BLOB NOT NULL,
            place_id INTEGER NOT NULL,
			req_ip TEXT,
            FOREIGN KEY (place_id) REFERENCES places (id)
        );
    `)
	if err != nil {
		fmt.Println(err)
		return
	}

	http.HandleFunc("/submit/{id}", handleSubmit)
	http.HandleFunc("/view/{id}", handleView)
	http.HandleFunc("/img/{id}", handleFile)

	http.ListenAndServe(":8080", nil)
}

func getReqIP(r *http.Request) string {
	//log.Debug(r.Header)

	cfConnectingIP := r.Header.Get("Cf-Connecting-Ip")
	if cfConnectingIP != "" {
		return cfConnectingIP
	}

	// Check for the X-Forwarded-For header
	xForwardedFor := r.Header.Get("X-Forwarded-For")
	if xForwardedFor != "" {
		// If the header is present, return the first IP address in the list
		// (in case there are multiple proxies)
		return strings.Split(xForwardedFor, ",")[0]
	}

	// Check for the X-Real-IP header (used by some proxies like Cloudflare)
	xRealIP := r.Header.Get("X-Real-IP")
	if xRealIP != "" {
		return xRealIP
	}

	// If no proxy headers are present, return the RemoteAddr
	return r.RemoteAddr
}

func handleView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	rows, err := db.Query("SELECT id, timestamp, image_data, place_id FROM log_data WHERE place_id = ? ORDER BY id DESC", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var logData []LogData
	for rows.Next() {
		var ld LogData
		err = rows.Scan(&ld.ID, &ld.Timestamp, &ld.ImageData, &ld.PlaceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logData = append(logData, ld)
	}
	tmpl := template.Must(template.New("dirview.html").Funcs(template.FuncMap{
		"formatTimestamp": func(t time.Time) string {
			return t.Local().UTC().Format(time.RFC3339)
		},
	}).ParseFiles("dirview.html"))

	err = tmpl.Execute(w, struct {
		LogData []LogData
	}{
		LogData: logData,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func handleFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	row := db.QueryRow("SELECT image_data FROM log_data WHERE id = ? LIMIT 1", id)

	var imageData []byte
	err := row.Scan(&imageData)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "No image found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(imageData)
}

func handleSubmit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Accept-Language, Accept-Encoding, Authorization")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Header.Get("Authorization") != TOKEN {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id == 0 {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()
	data, err = base64.StdEncoding.DecodeString(string(data))

	if !CheckForPlace(id) {
		InsertNewPlace(id)
	}

	hip := md5.New()
	_, err = hip.Write([]byte(getReqIP(r)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	ip := base64.RawStdEncoding.EncodeToString(hip.Sum(nil))

	c, ok := Cooldowns[id]
	if !ok || time.Now().Unix() > c.NextTime.Unix() {
		log.Debug("Created a new cooldown", "id", id)
		Cooldowns[id] = CooldownData{NextTime: time.Now().Add(CooldownTime)}
		go SubmitCooldown(id)
		AppendLog(data, id, ip)
	} else {
		log.Debug("Appended to cooldown", "tleft", time.Until(c.NextTime))
		c.Pending = data
		c.LastIP = ip
	}

	fmt.Printf("Got new submission for %d @ %s by %s\n",
		id, time.Now().UTC().Format(time.RFC3339), ip)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

}
