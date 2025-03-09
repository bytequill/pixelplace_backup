package main

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"gopkg.in/gographics/imagick.v3/imagick"
)

func apiPlaceItems(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	id := r.PathValue("id")
	start := r.FormValue("start")
	startd, err := strconv.Atoi(start) // Returns 0 on error which is what we want
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

	if r.Header.Get("Authorization") != TOKEN {
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
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

}

func apiFile(w http.ResponseWriter, r *http.Request) {
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

func apiDiff(w http.ResponseWriter, r *http.Request) {
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
