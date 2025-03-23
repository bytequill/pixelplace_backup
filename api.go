package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
)

func apiPlaceItems(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	id := r.PathValue("id")
	start := r.FormValue("start")
	startd, err := strconv.Atoi(start)
	if err != nil {
		startd = math.MaxInt64
	}

	rows, err := db.Query(`SELECT id, timestamp, image_data, place_id
							FROM log_data
							WHERE place_id = ? AND id < ?
							ORDER BY id DESC
							LIMIT 50;`, id, startd)
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

	if len(logData) == 0 {
		return
	}

	for i, l := range logData {
		fmt.Fprintf(w, `<li class="item">
<a href="/api/img/%d" target="_blank"><img src="/api/img/%d"></a>`, l.ID, l.ID)

		if i < len(logData)-1 {
			next := logData[i+1].ID
			fmt.Fprintf(w, `<a href="/api/diff/%d/%d" target="_blank">Diff</a>`, l.ID, next)
		} else {
			fmt.Fprintf(w, `<a class="disabled">Diff</a>`)
		}

		fmt.Fprintf(w, `<p style="margin: 0;">%s; ID: %d</p>`, l.Timestamp.Format(time.RFC3339), l.ID)
		fmt.Fprintf(w, "</li>")
	}

	newStart := logData[len(logData)-1].ID
	// The negative margin makes the loading smoother for the user
	fmt.Fprintf(w, `<div id="load-more" style="margin-top: -300px"
			hx-get="/api/placeItems/%d?start=%d"
			hx-trigger="revealed"
			hx-swap="outerHTML"
			></div>`, logData[0].PlaceID, newStart)
}

// Non HTMX routes

func apiSubmit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Accept-Language, Accept-Encoding, Authorization")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if TOKEN != "" && r.Header.Get("Authorization") != TOKEN {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id == 0 {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if id == 7 {
		http.Error(w, "Blacklisted place", http.StatusForbidden)
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
		return
	}

	if !CheckForPlace(id) {
		InsertNewPlace(id)
	}

	hip := md5.New()
	_, err = hip.Write([]byte(getReqIP(r)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ip := base64.RawStdEncoding.EncodeToString(hip.Sum(nil))

	c, ok := Cooldowns[id]
	if !ok || time.Now().Unix() > c.NextTime.Unix() {
		log.Debug("Created a new cooldown", "id", id)
		Cooldowns[id] = &CooldownData{NextTime: time.Now().Add(CooldownTime), Pending: data, LastIP: ip}
		go SubmitCooldown(id)
		c.Pending = data
		c.LastIP = ip
		//AppendLog(data, id, ip)
	} else {
		log.Debug("Appended to cooldown", "tleft", time.Until(c.NextTime))
		c.Pending = data
		c.LastIP = ip
	}

	fmt.Printf("Got new submission for %d @ %s by %s\n",
		id, time.Now().UTC().Format(time.RFC3339), ip)
}

func apiFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	imageData, err := getFileByID(id)
	if err != nil && err.Error() == "not found" {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(imageData)
}

func apiDiff(w http.ResponseWriter, r *http.Request) {
	idOne := r.PathValue("id1")
	idTwo := r.PathValue("id2")
	if idOne == "" || idTwo == "" {
		http.Error(w, "Id1 and Id2 not included", http.StatusBadRequest)
		return
	}

	imgdataOne, err := getFileByID(idOne)
	if err != nil {
		if err.Error() == "not found" {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	imgdataTwo, err := getFileByID(idTwo)
	if err != nil {
		if err.Error() == "not found" {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	imgOne, _, err := image.Decode(bytes.NewReader(imgdataOne))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	imgTwo, _, err := image.Decode(bytes.NewReader(imgdataTwo))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if imgOne.Bounds().String() != imgTwo.Bounds().String() {
		http.Error(w, "Mismatch in selected image size", http.StatusUnprocessableEntity)
	}
	resultImg := image.NewRGBA(imgOne.Bounds())

	rgbaOne := image.NewRGBA(imgOne.Bounds())
	draw.Draw(rgbaOne, rgbaOne.Bounds(), imgOne, image.ZP, draw.Src)

	rgbaTwo := image.NewRGBA(imgTwo.Bounds())
	draw.Draw(rgbaTwo, rgbaTwo.Bounds(), imgTwo, image.ZP, draw.Src)

	// Draws different pixels as red and non-differing pixels as desaturated originals
	for dx := range resultImg.Bounds().Dx() {
		for dy := range resultImg.Bounds().Dy() {
			pixOne := color.GrayModel.Convert(rgbaOne.At(dx, dy)).(color.Gray)
			pixTwo := color.GrayModel.Convert(rgbaTwo.At(dx, dy)).(color.Gray)
			if pixOne.Y != pixTwo.Y {
				resultImg.Set(dx, dy, color.RGBA{255, 0, 0, 255})
			} else {
				r, g, b, _ := rgbaOne.At(dx, dy).RGBA()
				resultImg.Set(dx, dy, color.RGBA{uint8(r), uint8(g), uint8(b), 255 * 0.4})
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	png.Encode(w, resultImg)
}

func apiTimelapse(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	idOne := r.PathValue("id1")
	idTwo := r.PathValue("id2")
	dval := r.FormValue("delay")
	if idOne == "" || idTwo == "" {
		http.Error(w, "Id1 and Id2 not included", http.StatusBadRequest)
		return
	}
	var delay int
	var err error // Prevents a compiler eror
	if dval == "" {
		delay = 20
	} else {
		delay, err = strconv.Atoi(dval)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	var placeID int
	err = db.QueryRow(`SELECT place_id FROM log_data WHERE id = ?`, idOne).Scan(&placeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := db.Query(`SELECT timestamp, image_data
							FROM log_data
							WHERE id <= ? AND id >= ? AND place_id = ?
							ORDER BY id ASC;`, idOne, idTwo, placeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var logData []LogData
	for rows.Next() {
		var ld LogData
		err = rows.Scan(&ld.Timestamp, &ld.ImageData)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logData = append(logData, ld)
	}

	if len(logData) <= 2 {
		http.Error(w, "Not enough frames", http.StatusBadRequest)
		return
	}

	anim := gif.GIF{LoopCount: len(logData)}
	for _, l := range logData {
		frame, err := png.Decode(bytes.NewReader(l.ImageData))
		if err != nil {
			http.Error(w, "Invalid frame detected;"+err.Error(), http.StatusInternalServerError)
			return
		}

		paletted := image.NewPaletted(frame.Bounds(), palette.Plan9)
		draw.Draw(paletted, frame.Bounds(), frame, image.ZP, draw.Src)

		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, delay) // in c(enti)s
	}

	err = gif.EncodeAll(w, &anim)
	if err != nil {
		log.Errorf("Error encoding GIF;%s", err)
		http.Error(w, "Cannot encode GIF;"+err.Error(), http.StatusInternalServerError)
		return
	}
}
