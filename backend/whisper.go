package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"

	"github.com/google/uuid"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

const (
	whisperBin       = "whisper.cpp/main"
	whisperModelPath = "whisper.cpp/models/ggml-"
	samplesDir       = "whisper.cpp/samples"
)

func getSubsFile(w http.ResponseWriter, r *http.Request) {
	path, err := os.Getwd()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		returnServerError(w, r, fmt.Sprintf("Error getting path: %v", err))
		return
	}
	id := r.URL.Query().Get("id")

	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		returnServerError(w, r, fmt.Sprintf("ID does not exist: %v", err))
		return
	}

	fmt.Println(id)
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote("subtitles.srt"))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, fmt.Sprintf("%v/%v/%v.wav.srt", path, samplesDir, id))
	if KeepFiles != "true" {
		err = os.Remove(fmt.Sprintf("%v/%v/%v.wav.srt", path, samplesDir, id))
		if err != nil {
			log.Printf("Could not remove the .wav file %v.", err)
		}
	}
}

func returnServerError(w http.ResponseWriter, r *http.Request, message string) {
	var response Response
	response.Message = message
	response.Result = ""
	response.Id = ""

	log.Printf("ERROR: %v", message)
	jsonData, err := json.Marshal(response)
	if err != nil {
		log.Printf("Error marshalling to json: %v", err)
		return
	}

	//w.WriteHeader(http.StatusInternalServerError)
	w.Write(jsonData)
}

func transcribe(w http.ResponseWriter, r *http.Request) {
	path, err := os.Getwd()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		returnServerError(w, r, fmt.Sprintf("Error getting path: %v", err))
		return
	}

	switch r.Method {
	case "GET":
		var response Response
		response.Result = "Not allowed"
		jsonData, err := json.Marshal(response)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Printf("Error marshalling tasks to json: %v", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(jsonData)
		return
	case "POST":
		log.Printf("Got POST for transcribing...")
		var response Response
		response.Message = ""
		response.Result = ""
		response.Id = ""

		file, header, err := r.FormFile("file")
		log.Printf("Got file %v...", header.Filename)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			returnServerError(w, r, fmt.Sprintf("Error getting the form file: %v", err))
			return
		}
		defer file.Close()

		language := r.FormValue("lang")
		if language == "" {
			fmt.Println("Defaulting language to English...")
			language = "en"
		}

		// get params
		translate, _ := strconv.ParseBool(r.FormValue("translate"))
		getSubs, _ := strconv.ParseBool(r.FormValue("subs"))
		speedUp, _ := strconv.ParseBool(r.FormValue("speedUp"))

		id := uuid.New()
		f, err := os.OpenFile(fmt.Sprintf("%v/%v/%v.webm", path, samplesDir, id.String()), os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			returnServerError(w, r, fmt.Sprintf("Error getting the form file: %v", err))
			return
		}
		defer f.Close()
		io.Copy(f, file)

		/*** FFMPEG ****/

		ffmpegArgs := make([]ffmpeg.KwArgs, 0)
		// Load .env variables
		if CutMediaSeconds != "0" {
			ffmpegArgs = append(ffmpegArgs, ffmpeg.KwArgs{"t": CutMediaSeconds})
		}

		// Append all args and merge to single KwArgs
		ffmpegArgs = append(ffmpegArgs, ffmpeg.KwArgs{"ar": 16000, "ac": 1, "c:a": "pcm_s16le"})
		args := ffmpeg.MergeKwArgs(ffmpegArgs)

		err = ffmpeg.Input(fmt.Sprintf("%v/%v/%v.webm", path, samplesDir, id.String())).
			Output(fmt.Sprintf("%v/%v/%v.wav", path, samplesDir, id.String()), args).
			OverWriteOutput().ErrorToStdOut().Run()

		if err != nil {
			log.Printf("%v", err)
			w.WriteHeader(http.StatusInternalServerError)
			returnServerError(w, r, fmt.Sprintf("Error while encoding to wav: %v", err))
			return
		}

		// Remove old file
		err = os.Remove(fmt.Sprintf("%v/%v/%v.webm", path, samplesDir, id.String()))
		if err != nil {
			log.Printf("Could not remove file.")
		}

		/*** WHISPER ****/
		// Prepare whisper main args
		commandString := fmt.Sprintf("%v/%v", path, whisperBin)
		targetFilepath := fmt.Sprintf("%v/%v/%v.wav", path, samplesDir, id.String())
		model := fmt.Sprintf("%v/%v%v.bin", path, whisperModelPath, WhisperModel)

		// Populate whisper args
		whisperArgs := make([]string, 0)
		whisperArgs = append(whisperArgs, "-m", model, "-nt", "-l", language)
		if getSubs {
			whisperArgs = append(whisperArgs, "-osrt")
		}
		if speedUp { // Speed Up
			whisperArgs = append(whisperArgs, "--speed-up")
		}
		if translate {
			whisperArgs = append(whisperArgs, "--translate")
		}
		fmt.Println(WhisperThreads, WhisperProcs)
		if WhisperThreads != "4" {
			whisperArgs = append(whisperArgs, "-t", WhisperThreads)
		}
		if WhisperProcs != "1" {
			whisperArgs = append(whisperArgs, "-p", WhisperProcs)
		}

		whisperArgs = append(whisperArgs, "-f", targetFilepath)

		// Run whisper
		log.Printf("%v %v", commandString, whisperArgs)
		command := exec.Command(commandString, whisperArgs...)
		fmt.Printf(command.String())
		output, err := exec.Command(commandString, whisperArgs...).Output()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			returnServerError(w, r, fmt.Sprintf("Error while transcribing: %v", err))
			return
		}

		response.Result = string(output)
		response.Id = id.String()

		jsonData, err := json.Marshal(response)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			returnServerError(w, r, fmt.Sprintf("Error marshalling to json: %v", err))
			return
		}

		if KeepFiles != "true" {
			err = os.Remove(fmt.Sprintf("%v/%v/%v.wav", path, samplesDir, id.String()))
			if err != nil {
				log.Printf("Could not remove the .wav file %v.", err)
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write(jsonData)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
}
