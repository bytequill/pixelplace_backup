package main

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"image"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"image/draw"
	_ "image/png"

	"github.com/charmbracelet/log"
	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/gographics/imagick.v3/imagick"
)

// Default values
var TOKEN string = ""             // No header check!
var BLACKLIST_ID []int = []int{7} // Space separated values in ENV_VAR
var PORT int = 9899
var DEBUG bool = false

var db *sql.DB

const CooldownTime time.Duration = 10*time.Minute - 20*time.Second

var Cooldowns map[int]*CooldownData = make(map[int]*CooldownData)

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

// go embed stuff

//go:embed static
var staticFS embed.FS

//go:embed templates
var templatesFS embed.FS

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

func getLatestFile(id int) ([]byte, error) {
	row := db.QueryRow("SELECT image_data FROM log_data WHERE place_id = ? ORDER BY id DESC LIMIT 1", id)

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

	imgnow, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	lastimgdata, err := getLatestFile(placeID)
	if err != nil {
		return
	}
	LastImg, _, err := image.Decode(bytes.NewReader(lastimgdata))
	if err != nil {
		return
	}

	rgbaOne := image.NewRGBA(imgnow.Bounds())
	draw.Draw(rgbaOne, rgbaOne.Bounds(), imgnow, image.ZP, draw.Src)

	rgbaTwo := image.NewRGBA(LastImg.Bounds())
	draw.Draw(rgbaTwo, rgbaTwo.Bounds(), LastImg, image.ZP, draw.Src)

	d, err := FastImgCompare(rgbaOne, rgbaTwo)

	log.Debug("New image submited with", "diff", d)

	if d < 10 && err == nil {
		log.Warn("Diff too low", "d", d)
		return
	}

	_, err = db.Exec("INSERT INTO log_data (image_data, place_id, req_ip) VALUES (?, ?, ?)", data, placeID, ip)
	if err != nil {
		fmt.Println(err)
		return
	}
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

func FastImgCompare(img1, img2 *image.RGBA) (float64, error) {
	if img1.Bounds() != img2.Bounds() {
		return 0, fmt.Errorf("image bounds not equal: %+v, %+v", img1.Bounds(), img2.Bounds())
	}

	mse := 0.0
	totalPixels := len(img1.Pix) / 4

	for i := 0; i < totalPixels; i++ {
		r1, g1, b1, a1 := float64(img1.Pix[i*4]), float64(img1.Pix[i*4+1]), float64(img1.Pix[i*4+2]), float64(img1.Pix[i*4+3])
		r2, g2, b2, a2 := float64(img2.Pix[i*4]), float64(img2.Pix[i*4+1]), float64(img2.Pix[i*4+2]), float64(img2.Pix[i*4+3])

		mse += (r1-r2)*(r1-r2) + (g1-g2)*(g1-g2) + (b1-b2)*(b1-b2) + (a1-a2)*(a1-a2)
	}

	mse /= float64(totalPixels * 4)

	return mse, nil
}

func main() {
	if os.Getenv("TOKEN") != "" {
		TOKEN = os.Getenv("TOKEN")
	} else {
		log.Warn("Running without submit authorization!")
	}
	if strings.ToLower(os.Getenv("DEBUG")) == "true" {
		DEBUG = true
		log.SetLevel(log.DebugLevel)
		log.SetReportCaller(true)

		log.Debug("Running in verbose mode. DEBU(G) messages enabled")
	}
	portenv := os.Getenv("PORT")
	if portenv != "" {
		portv, err := strconv.Atoi(portenv)
		if err == nil && portv <= math.MaxUint16 {
			PORT = portv
		} else {
			log.Warn("Invalid port value", "v", portenv)
		}
	}
	blacklistenv := os.Getenv("BLACKLIST_ID")
	if blacklistenv != "" {
		parts := strings.Fields(blacklistenv)
		BLACKLIST_ID = make([]int, 0)
		for i, p := range parts {
			id, err := strconv.Atoi(p)
			if err != nil {
				log.Warn("Invalid ID in BLACKLIST_ID", "pos", i, "v", p)
				continue
			}
			BLACKLIST_ID = append(BLACKLIST_ID, id)
		}
		if len(BLACKLIST_ID) == 0 {
			log.Warn("No blacklist IDs selected. Please make sure this is expected")
		}
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

	//Frontend
	http.HandleFunc("/view/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, templatesFS, "templates/view.html")
	})
	http.Handle("/", http.FileServer(http.FS(staticFS)))

	//API
	http.HandleFunc("/api/submit/{id}", apiSubmit)
	http.HandleFunc("/submit/{id}", apiSubmit) // Handle legacy submit route. Deprecated
	http.HandleFunc("/api/img/{id}", apiFile)
	http.HandleFunc("/api/diff/{id1}/{id2}", apiDiff)
	http.HandleFunc("/api/timelapse/{id1}/{id2}", apiTimelapse)

	//HTMX stuff
	http.HandleFunc("/api/placeItems/{id}", apiPlaceItems)

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
