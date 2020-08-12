//@author:llychao<lychao_vip@163.com>
//@date:2020-02-18
//@功能:golang m3u8 video Downloader
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"flag"
	"fmt"
	"github.com/levigross/grequests"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	//HeadTimeout 请求头超时时间
	HEAD_TIMEOUT = 10 * time.Second
)

var (
	//命令行参数
	urlFlag = flag.String("u", "", "m3u8下载地址(http(s)://url/xx/xx/index.m3u8)")
	nFlag   = flag.Int("n", 16, "下载线程数(max goroutines num)")
	htFlag  = flag.String("ht", "apiv1", "设置getHost的方式(apiv1: `http(s):// + url.Host + path.Dir(url.Path)`; apiv2: `http(s)://+ u.Host`")
	oFlag   = flag.String("o", "output", "定义存放的目录前序(目录名为output)")

	logger *log.Logger
	ro     = &grequests.RequestOptions{
		UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_13_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.88 Safari/537.36",
		RequestTimeout: HEAD_TIMEOUT,
		Headers: map[string]string{
			"Connection":      "keep-alive",
			"Accept":          "*/*",
			"Accept-Encoding": "*",
			"Accept-Language": "zh-Hans;q=1",
		},
	}
)

//TsInfo 用于保存ts文件的下载地址和文件名
type TsInfo struct {
	Name string
	Url  string
}

func init() {
	logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
}

func main() {
	Run()
}

func Run() {
	msgTpl := "[功能]:多线程下载直播流m3u8的视屏（ts+合并）\n[提醒]:如果下载失败，请使用-ht定义getHost的类型\n[提醒]:m3u8不要嵌套"
	fmt.Println(msgTpl)
	runtime.GOMAXPROCS(runtime.NumCPU())
	now := time.Now()

	//解析命令行参数
	flag.Parse()
	m3u8Url := *urlFlag
	maxGoroutines := *nFlag
	hostType := *htFlag
	movieDir := *oFlag

	if !strings.HasPrefix(m3u8Url, "http") || !strings.Contains(m3u8Url, "m3u8") || m3u8Url == "" {
		flag.Usage()
		return
	}

	var download_dir string
	pwd, _ := os.Getwd()
	//pwd = "/Users/chao/Desktop" //自定义地址
	download_dir = pwd + "/movie/" + movieDir
	if isExist, _ := PathExists(download_dir); !isExist {
		os.MkdirAll(download_dir, os.ModePerm)
	} else {
		download_dir = pwd + "/movie/" + movieDir + time.Now().Format("0601021504")
		os.MkdirAll(download_dir, os.ModePerm)
	}

	m3u8Host := getHost(m3u8Url, hostType)
	m3u8Body := getM3u8Body(m3u8Url)
	//m3u8Body := getFromFile()

	ts_key := getM3u8Key(m3u8Host, m3u8Body)
	if ts_key != "" {
		logger.Printf("待解密ts文件key: %s", ts_key)
	}

	ts_list := getTsList(m3u8Host, m3u8Body)
	logger.Println("待下载ts文件数量:", len(ts_list))

	downloader(ts_list, maxGoroutines, download_dir, ts_key)
	logger.Printf("下载完成，耗时:%#vs\n", time.Now().Sub(now).Seconds())

	switch runtime.GOOS {
	case "windows":
		win_merge_file(download_dir)
	default:
		unix_merge_file(download_dir)
	}

	logger.Printf("任务完成，耗时:%#vs\n", time.Now().Sub(now).Seconds())
}

//获取m3u8地址的host
func getHost(Url, ht string) (host string) {
	u, err := url.Parse(Url)
	checkErr(err)
	switch ht {
	case "apiv1":
		host = u.Scheme + "://" + u.Host + path.Dir(u.Path)
		logger.Printf("host = %s", host)
	case "apiv2":
		host = u.Scheme + "://" + u.Host
		logger.Printf("host = %s", host)
	}
	return
}

//获取m3u8地址的内容体
func getM3u8Body(Url string) string {
	r, err := grequests.Get(Url, ro)
	checkErr(err)
	return r.String()
}

//获取m3u8加密的密钥
func getM3u8Key(host, html string) (key string) {
	lines := strings.Split(html, "\n")
	key = ""
	for _, line := range lines {
		if strings.Contains(line, "#EXT-X-KEY") {
			uri_pos := strings.Index(line, "URI")
			quotation_mark_pos := strings.LastIndex(line, "\"")
			key_url := strings.Split(line[uri_pos:quotation_mark_pos], "\"")[1]
			if !strings.Contains(line, "http") {
				key_url = fmt.Sprintf("%s/%s", host, key_url)
			}
			logger.Println("key_url:", key_url)
			res, err := grequests.Get(key_url, ro)
			checkErr(err)
			if res.StatusCode == 200 {
				key = res.String()
			}
		}
	}
	return
}

func getTsList(host, body string) (tsList []TsInfo) {
	lines := strings.Split(body, "\n")
	index := 0
	var ts TsInfo

	for _, line := range lines {
		if !strings.HasPrefix(line, "#") && line != "" {
			//有可能出现的二级嵌套格式的m3u8,请自行转换！
			index++
			if strings.HasPrefix(line, "http") {
				ts = TsInfo{
					Name: fmt.Sprintf("%05d.ts", index),
					Url:  line,
				}
				tsList = append(tsList, ts)
			} else {
				ts = TsInfo{
					Name: fmt.Sprintf("%05d.ts", index),
					Url:  fmt.Sprintf("%s/%s", host, line),
				}
				tsList = append(tsList, ts)
			}
		}
	}
	return
}

//判断文件是否存在
func PathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func getFromFile() string {
	data, _ := ioutil.ReadFile("./ts.txt")
	return string(data)
}

//下载ts文件
func downloadTsFile(ts TsInfo, download_dir, key string, retries int) {
	curr_path := fmt.Sprintf("%s/%s", download_dir, ts.Name)
	if isExist, _ := PathExists(curr_path); isExist {
		logger.Println("[warn] File: " + ts.Name + "already exist")
		return
	}
	res, err := grequests.Get(ts.Url, ro)
	if err != nil || !res.Ok {
		if retries > 0 {
			downloadTsFile(ts, download_dir, key, retries-1)
			return
		} else {
			logger.Printf("[warn] File :%s, Retry %s", ts.Url, retries-1)
			return
		}
	}

	if key == "" {
		res.DownloadToFile(curr_path)
	} else {
		//若加密，解密ts文件 aes 128 cbc pack5
		origData, err := AesDecrypt(res.Bytes(), []byte(key))
		if err != nil {
			downloadTsFile(ts, download_dir, key, retries-1)
			return
		}
		ioutil.WriteFile(curr_path, origData, 0666)
	}
}

//Downloader m3u8下载器
func downloader(tsList []TsInfo, maxGoroutines int, downloadDir string, key string) {
	retry := 5
	var wg sync.WaitGroup
	limiter := make(chan int, maxGoroutines)

	for _, ts := range tsList {
		wg.Add(1)
		limiter <- 1
		go func(ts TsInfo, downloadDir, key string, retryies int) {
			defer func() {
				wg.Done()
				<-limiter
			}()
			downloadTsFile(ts, downloadDir, key, retryies)
			return
		}(ts, downloadDir, key, retry)
	}
	wg.Wait()
}

//执行shell
func ExecUnixShell(s string) {
	cmd := exec.Command("/bin/bash", "-c", s)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s", out.String())
}

func ExecWinShell(s string) error {
	cmd := exec.Command("cmd", "/C", s)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return err
	}
	fmt.Printf("%s", out.String())
	return nil
}

//windows合并文件
func win_merge_file(path string) {
	os.Chdir(path)
	ExecWinShell("copy /b *.ts new.tmp")
	ExecWinShell("del /Q *.ts")
	os.Rename("new.tmp", "new.mp4")
}

//unix合并文件
func unix_merge_file(path string) {
	os.Chdir(path)
	//cmd := `ls  *.ts |sort -t "\." -k 1 -n |awk '{print $0}' |xargs -n 1 -I {} bash -c "cat {} >> new.tmp"`
	cmd := `cat *.ts >> new.tmp`
	ExecUnixShell(cmd)
	ExecUnixShell("rm -rf *.ts")
	os.Rename("new.tmp", "new.mp4")
}

// ============================== 加解密相关 ==============================

func PKCS7Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

func PKCS7UnPadding(origData []byte) []byte {
	length := len(origData)
	unpadding := int(origData[length-1])
	return origData[:(length - unpadding)]
}

func AesEncrypt(origData, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	origData = PKCS7Padding(origData, blockSize)
	blockMode := cipher.NewCBCEncrypter(block, key[:blockSize])
	crypted := make([]byte, len(origData))
	blockMode.CryptBlocks(crypted, origData)
	return crypted, nil
}

func AesDecrypt(crypted, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	blockMode := cipher.NewCBCDecrypter(block, key[:blockSize])
	origData := make([]byte, len(crypted))
	blockMode.CryptBlocks(origData, crypted)
	origData = PKCS7UnPadding(origData)
	return origData, nil
}

func checkErr(e error) {
	if e != nil {
		logger.Panic(e)
	}
}
