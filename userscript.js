// ==UserScript==
// @name         Backup canvas
// @namespace    http://tampermonkey.net/
// @version      2025-03-03
// @description  try to take over the world!
// @author       github.com/bytequill
// @match        https://pixelplace.io/*
// @icon         https://www.google.com/s2/favicons?sz=64&domain=pixelplace.io
// @grant        none
// ==/UserScript==
const SURL = "https://pixelplace.codebased.xyz/submit";
const STOKEN = "ILOVEKISSINGBOYS";
const SINTERVAL = 600_000
var lastdata = "";

function fetchData() {
    let data = document.getElementById("canvas").toDataURL();
    console.log(data);
    return data;
}

function submitData() {
    let id = extractPlaceID(document.location);
    let data = fetchData();

    if (lastdata == data) {
        console.warn("duplicate found. not sending")
        return
    }
    lastdata = data

    fetch(`${SURL}/${id}`, {
        method: "POST",
        headers: {
            "Authorization": STOKEN,
            "Content-Type": "image/png" // or application/octet-stream
        },
        body: stripImageHeader(data)
    })
    .then(response => {
        if (response.ok) {
            console.debug("Data submitted successfully");
        } else {
            console.error("Error submitting data:", response.status);
        }
    })
    .catch(error => {
        console.error("Error submitting data:", error);
    });
}

function extractPlaceID(urlString) {
    const url = new URL(urlString);
    const path = url.pathname;
    const parts = path.split('/');
    const placeIDString = parts[parts.length - 1].split('-')[0];
    return placeIDString;
}

function stripImageHeader(data) {
    let strippedData = data.replace("data:image/png;base64,", "");
    return strippedData
}

function mainBackup() {
     setTimeout(function() {
        submitData();
        setInterval(submitData, SINTERVAL);
     }, 5000); // 5 seconds
}

(function () {
  mainBackup();
})();