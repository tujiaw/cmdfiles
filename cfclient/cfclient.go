package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tujiaw/goutil"
)

const maxUploadSize = 5 * 1024 * 1024

var tmpDir = filepath.Join(os.TempDir(), "cmdfiles")
var configPath = filepath.Join(tmpDir, "config.json")
var config = NewConfig()

type Config struct {
	Host string `json:"host"`
	Port string `json:"port"`
}

func NewConfig() Config {
	result := new(Config)
	b, err := ioutil.ReadFile(configPath)
	if err != nil {
		return *result
	}
	json.Unmarshal(b, result)
	return *result
}

func (c Config) save(host string, port string) error {
	fmt.Println("save config host:", host, "port:", port)
	c.Host = host
	c.Port = port
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(configPath, b, os.ModePerm)
}

func (c Config) address() string {
	return fmt.Sprintf("http://%s:%s", c.Host, c.Port)
}

func (c Config) uploadUrl(path string) string {
	tmp := c.append(c.address(), "upload")
	return c.append(tmp, path)
}

func (c Config) deleteUrl(path string) string {
	tmp := c.append(c.address(), "delete")
	return c.append(tmp, path)
}

func (c Config) downloadUrl(path string) string {
	tmp := c.append(c.address(), "files")
	return c.append(tmp, path)
}

func (c Config) listUrl(path string) string {
	tmp := c.append(c.address(), "list")
	return c.append(tmp, path)
}

func (c Config) append(first string, second string) string {
	if len(second) > 0 {
		if second[0] != '/' {
			return first + "/" + second
		}
	}
	return first + second
}

func init() {
	if !goutil.Exists(tmpDir) {
		if err := os.Mkdir(tmpDir, os.ModePerm); err != nil {
			panic(err)
		}
	}

	tmp, _ := os.Open(tmpDir)
	names, _ := tmp.Readdirnames(-1)
	for _, name := range names {
		tmpPath := filepath.Join(tmpDir, name)
		if tmpPath != configPath {
			os.RemoveAll(tmpPath)
		}
	}
}

func main() {
	start := time.Now()
	if len(os.Args) == 1 || os.Args[1] == "help" {
		fmt.Println("command error")
		fmt.Println("usage: app config [<args>]")
		fmt.Println("usage: app upload [<args>]")
		fmt.Println("usage: app down [<args>]")
		fmt.Println("usage: app delete [<args>]")
		fmt.Println("usage: app list [<args>]")
		return
	}

	configCommand := flag.NewFlagSet("config", flag.ExitOnError)
	hostPtr := configCommand.String("host", "localhost", "remote host")
	portPtr := configCommand.String("port", "8081", "remote port")

	uploadCommand := flag.NewFlagSet("upload", flag.ExitOnError)
	uploadFromPtr := uploadCommand.String("from", "", "local file path")
	uploadToPtr := uploadCommand.String("to", "", "remote host path")

	downloadCommand := flag.NewFlagSet("down", flag.ExitOnError)
	downloadFromPtr := downloadCommand.String("from", "", "remote file path")
	downloadToPtr := downloadCommand.String("to", "", "local file dir")

	deleteCommand := flag.NewFlagSet("delete", flag.ExitOnError)
	deleteFromPtr := deleteCommand.String("from", "", "remote file path")

	listCommand := flag.NewFlagSet("list", flag.ExitOnError)
	listFromPtr := listCommand.String("from", "", "remote file path")

	cmds := map[string]*flag.FlagSet{}
	cmds["config"] = configCommand
	cmds["upload"] = uploadCommand
	cmds["down"] = downloadCommand
	cmds["delete"] = deleteCommand
	cmds["list"] = listCommand

	cmd, exist := cmds[os.Args[1]]
	if exist {
		if len(os.Args) > 2 && os.Args[2] == "help" {
			cmd.PrintDefaults()
			return
		}
		cmd.Parse(os.Args[2:])
	} else {
		fmt.Printf("%q is not valid command.\n", os.Args[1])
		os.Exit(2)
	}

	if !configCommand.Parsed() {
		if config.Host == "" || config.Port == "" {
			panic("please set config: app config help")
		}
	}

	if configCommand.Parsed() {
		config.save(*hostPtr, *portPtr)
	} else if uploadCommand.Parsed() {
		uploadFileHandle(*uploadFromPtr, *uploadToPtr)
	} else if downloadCommand.Parsed() {
		downloadFileHandle(*downloadFromPtr, *downloadToPtr)
	} else if deleteCommand.Parsed() {
		DeleteFileHandle(*deleteFromPtr)
	} else if listCommand.Parsed() {
		ListFileHandle(*listFromPtr)
	}

	fmt.Println("------", time.Since(start), "------")
}

func uploadFileHandle(from string, to string) {
	if len(from) == 0 {
		panic(errors.New("local file path is empty!"))
	}

	f, err := os.Stat(from)
	if err != nil {
		panic(err)
	}

	if f.IsDir() {
		panic(fmt.Errorf("%s is not file", from))
	}

	filename := from
	fileSize := goutil.GetFileSize(filename)
	if fileSize <= 0 {
		panic("file size error")
	}

	fields := map[string]string{
		"filename": filepath.Base(filename),
		"dir":      to,
	}

	if fileSize < maxUploadSize {
		err := postFile(filename, config.uploadUrl(to), fields)
		goutil.PanicIfErr(err)
	} else {
		pathChan := make(chan string, 5)
		go splitFile(filename, pathChan)
		index := 0
		for path := range pathChan {
			index += 1
			fields["multiindex"] = strconv.Itoa(index)
			err := postFile(path, config.uploadUrl(to), fields)
			goutil.PanicIfErr(err)
		}
	}
}

func downloadFileHandle(from string, to string) {
	if len(from) == 0 {
		panic(errors.New("remote file path is empty!"))
	}

	url := config.downloadUrl(from)
	resp, err := http.Get(url)
	goutil.PanicIfErr(err)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		if len(resp.Status) > 0 {
			fmt.Println(resp.Status)
			return
		}
		fmt.Println("STATUS CODE", resp.StatusCode)
		return
	}

	filename := from
	pos := strings.LastIndex(from, "/")
	if pos > 0 {
		filename = from[pos+1:]
	}

	to = filepath.Join(".", to, filename)
	goutil.DeleteFile(to)
	goutil.PanicIfErr(os.MkdirAll(filepath.Dir(to), os.ModePerm))

	fmt.Println("download from", url, "to", to)
	chanChunk := make(chan []byte, 5)
	go ReadChunk(resp.Body, maxUploadSize/2, chanChunk)
	total := 0
	for chunk := range chanChunk {
		total += len(chunk)
		goutil.PanicIfErr(goutil.WriteFileAppend(to, chunk))
		fmt.Printf("\r%d %s\t", total, goutil.FormatBytes(float64(total)))
	}

	fmt.Println()
	fmt.Println("SUCCESS")
}

func DeleteFileHandle(from string) {
	if len(from) == 0 {
		panic("from is empty")
	}

	resp, err := http.Get(config.deleteUrl(from))
	goutil.PanicIfErr(err)
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	goutil.PanicIfErr(err)
	fmt.Println(string(body))
}

func ListFileHandle(from string) {
	if len(from) == 0 {
		from = "/"
	}
	resp, err := http.Get(config.listUrl(from))
	goutil.PanicIfErr(err)
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	goutil.PanicIfErr(err)
	fmt.Println(string(body))
}

func splitFile(filename string, pathChan chan string) {
	f, err := os.Open(filename)
	goutil.PanicIfErr(err)
	if err != nil {
		return
	}
	defer f.Close()
	defer close(pathChan)

	index := 0
	buf := make([]byte, maxUploadSize)
	r := bufio.NewReader(f)
	uuidv4, _ := goutil.Uuidv4()
	for {
		n, err := r.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			return
		}
		if 0 == n {
			break
		}

		index += 1
		smallFileName := fmt.Sprintf("%s/%s-%d_%s", tmpDir, uuidv4, index, filepath.Base(filename))
		err = ioutil.WriteFile(smallFileName, buf[:n], os.ModePerm)
		if err != nil {
			return
		}
		pathChan <- smallFileName
	}
}

func postFile(filename string, targetUrl string, fileds map[string]string) error {
	fmt.Println("post", targetUrl, filename)
	bodyBuf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)
	fileWriter, err := bodyWriter.CreateFormFile("uploadFileHandle", filename)
	if err != nil {
		fmt.Println("error writing to buffer")
		return err
	}
	fh, err := os.Open(filename)
	if err != nil {
		fmt.Println("error opening file")
		return err
	}
	_, err = io.Copy(fileWriter, fh)
	if err != nil {
		return err
	}

	for k, v := range fileds {
		bodyWriter.WriteField(k, v)
	}
	contentType := bodyWriter.FormDataContentType()
	bodyWriter.Close()
	resp, err := http.Post(targetUrl, contentType, bodyBuf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	resp_body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	fmt.Println(string(resp_body))
	return nil
}

func ReadChunk(r io.Reader, maxChunkSize int, chanChunk chan<- []byte) {
	for {
		buf := make([]byte, maxChunkSize)
		n, err := r.Read(buf)
		if n < 0 {
			break
		}
		if err != nil && err != io.EOF {
			break
		}
		chanChunk <- buf[:n]
		if err == io.EOF {
			break
		}
	}
	close(chanChunk)
}
