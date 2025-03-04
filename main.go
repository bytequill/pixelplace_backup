package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/gographics/imagick.v3/imagick"
)

var db *sql.DB
var TOKEN string

const CooldownTime time.Duration = 10*time.Minute - 20*time.Second

var Cooldowns map[int]*CooldownData = make(map[int]*CooldownData)
var LastImgs map[int][]byte = make(map[int][]byte)

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

func getFileByID(id string) ([]byte, error) {
	row := db.QueryRow("SELECT image_data FROM log_data WHERE id = ? LIMIT 1", id)

	var imageData []byte
	err := row.Scan(&imageData)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("not found")
		}
		return nil, err
	}

	return imageData, nil
}

func AppendLog(data []byte, placeID int, ip string) {
	hd := md5.New()
	hd.Write(data)

	if string(hd.Sum(nil)) == string(LastImgs[placeID]) {
		return
	}
	_, err := db.Exec("INSERT INTO log_data (image_data, place_id, req_ip) VALUES (?, ?, ?)", data, placeID, ip)
	if err != nil {
		fmt.Println(err)
		return
	}

	hd = md5.New()
	hd.Write(data)
	LastImgs[placeID] = hd.Sum(nil)
}

func SubmitCooldown(id int) {
	c := Cooldowns[id]
	time.Sleep(time.Until(c.NextTime))

	if len(c.Pending) > 0 {
		AppendLog(c.Pending, id, c.LastIP)
		log.Debug("Submitted a cooldown", "ip", c.LastIP)
	} else {
		log.Debug("Cooldown empty")
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

	imagick.Initialize()
	defer imagick.Terminate()

	http.HandleFunc("/submit/{id}", handleSubmit)
	http.HandleFunc("/view/{id}", handleView)
	http.HandleFunc("/img/{id}", handleFile)
	http.HandleFunc("/diff/{id1}/{id2}", handleDiff)

	http.ListenAndServe(":9899", nil)
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
		"add": func(i int, n int) int {
			return i + n
		},
		"sub": func(i int, n int) int {
			return i - n
		},
	}).ParseFiles("dirview.html"))

	tmpl.Execute(w, struct {
		LogData []LogData
	}{
		LogData: logData,
	})
}

func handleFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	imageData, err := getFileByID(id)
	if err != nil && err.Error() == "not found" {
		http.Error(w, err.Error(), http.StatusNotFound)
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(imageData)
}

func handleDiff(w http.ResponseWriter, r *http.Request) {
	idOne := r.PathValue("id1")
	idTwo := r.PathValue("id2")
	if idOne == "" || idTwo == "" {
		http.Error(w, "Id1 and Id2 not included", http.StatusBadRequest)
		return
	}

	imgOne, err := getFileByID(idOne)
	if err != nil {
		if err.Error() == "not found" {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	imgTwo, err := getFileByID(idTwo)
	if err != nil {
		if err.Error() == "not found" {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	mw := imagick.NewMagickWand()
	defer mw.Destroy()

	if err := mw.ReadImageBlob(imgOne); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mwTwo := imagick.NewMagickWand()
	defer mwTwo.Destroy()

	if err := mwTwo.ReadImageBlob(imgTwo); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	res, _ := mw.CompareImages(mwTwo, imagick.METRIC_ABSOLUTE_ERROR)
	defer res.Destroy()

	imgdata, err := res.GetImageBlob()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(imgdata)
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
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}

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
		Cooldowns[id] = &CooldownData{NextTime: time.Now().Add(CooldownTime), Pending: data, LastIP: ip}
		go SubmitCooldown(id)
		//AppendLog(data, id, ip)
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
