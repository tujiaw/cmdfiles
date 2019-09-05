package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tujiaw/goutil"
)

const maxUploadSize = 10 * 1024 * 1024
const uploadPath = "./public"

func main() {
	portPtr := flag.String("p", "8081", "port")
	flag.Parse()

	port := *portPtr
	if !goutil.Exists(uploadPath) {
		if err := os.Mkdir(uploadPath, os.ModePerm); err != nil {
			panic(err)
		}
	}

	http.HandleFunc("/upload", uploadFileHandler())
	http.HandleFunc("/delete/", deleteFileHandler())

	fs := http.FileServer(http.Dir(uploadPath))
	http.Handle("/files/", http.StripPrefix("/files", fs))

	log.Println("listen on", port)
	err := http.ListenAndServe(":"+port, nil)
	goutil.PanicIfErr(err)
}

func uploadFileHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println(r.RequestURI)
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			renderError(w, "FILE_TOO_BIG", http.StatusBadRequest)
			return
		}

		fileName := r.PostFormValue("filename")
		dir := r.PostFormValue("dir")
		dir = filepath.Join(uploadPath, dir)
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			renderError(w, "INVALID_DIR", http.StatusBadRequest)
			return
		}

		newPath := filepath.Join(dir, fileName)
		multiindex, err := strconv.Atoi(r.PostFormValue("multiindex"))
		if err != nil {
			multiindex = 0
		}

		file, _, err := r.FormFile("uploadFile")
		if err != nil {
			renderError(w, "INVALID_FILE", http.StatusBadRequest)
			return
		}
		defer file.Close()

		fileBytes, err := ioutil.ReadAll(file)
		if err != nil {
			renderError(w, "INVALID_FILE", http.StatusBadRequest)
			return
		}

		if multiindex >= 1 {
			if multiindex == 1 {
				os.Remove(newPath)
			}
			err = goutil.WriteFileAppend(newPath, fileBytes)
			if err != nil {
				fmt.Println(err)
				renderError(w, "WRITE_FILE_APPEND_ERROR", http.StatusInternalServerError)
			}
		} else {
			err = ioutil.WriteFile(newPath, fileBytes, os.ModePerm)
			if err != nil {
				fmt.Println(err)
			}
		}
		w.Write([]byte("SUCCESS"))
	})
}

func deleteFileHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path[0:7] == "/delete" {
			deletePath := r.URL.Path[8:]
			if len(deletePath) == 0 {
				renderError(w, "INVALID_URL", http.StatusBadRequest)
				return
			}

			deletePath = filepath.Join(uploadPath, deletePath)
			fmt.Println("delete path:", deletePath)
			err := os.RemoveAll(deletePath)
			if err != nil {
				renderError(w, err.Error(), http.StatusInternalServerError)
			} else {
				w.Write([]byte("SUCCESS"))
			}
		} else {
			renderError(w, "INVALID_URL", http.StatusBadRequest)
		}
	})
}

func renderError(w http.ResponseWriter, message string, statusCode int) {
	log.Println("ERROR", message)
	w.WriteHeader(statusCode)
	w.Write([]byte(message))
}
