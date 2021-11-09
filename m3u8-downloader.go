//@author: llychao<lychao_vip@163.com>
//@contributor: Junyi<me@junyi.pw>
//@date: 2020-02-18
//@功能: golang m3u8 video Downloader
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"flag"
	"fmt"
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

	"github.com/levigross/grequests"
)

const (
	//HeadTimeout 请求头超时时间
	HeadTimeout = 10 * time.Second
	// ProgressWidth 进度条长度
	ProgressWidth = 40
)

var (
	//命令行参数
	urlFlag = flag.String("u", "", "m3u8下载地址(http(s)://url/xx/xx/index.m3u8)")
	nFlag   = flag.Int("n", 16, "下载线程数(max goroutines num)")
	htFlag  = flag.String("ht", "apiv1", "设置getHost的方式(apiv1: `http(s):// + url.Host + path.Dir(url.Path)`; apiv2: `http(s)://+ u.Host`")
	oFlag   = flag.String("o", "output", "自定义文件名(默认为output)")
	cFlag   = flag.String("c", "", "自定义请求cookie")
	sFlag   = flag.Int("s", 0, "是否允许不安全的请求(默认为0)")

	logger *log.Logger
	ro     = &grequests.RequestOptions{
		UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_13_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.88 Safari/537.36",
		RequestTimeout: HeadTimeout,
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
	msgTpl := "[功能]:多线程下载直播流m3u8的视屏（ts+合并）\n[提醒]:如果下载失败，请使用-ht=apiv2\n[提醒]:如果下载失败，m3u8地址可能存在嵌套\n[提醒]:如果进度条中途下载失败，可重复执行"
	fmt.Println(msgTpl)
	runtime.GOMAXPROCS(runtime.NumCPU())
	now := time.Now()

	//解析命令行参数
	flag.Parse()
	m3u8Url := *urlFlag
	maxGoroutines := *nFlag
	hostType := *htFlag
	movieDir := *oFlag
	cookie := *cFlag
	insecure := *sFlag

	if insecure != 0 {
		ro.InsecureSkipVerify = true
	}

	//http自定义cookie
	if cookie != "" {
		ro.Headers["Cookie"] = cookie
	}

	if !strings.HasPrefix(m3u8Url, "http") || !strings.Contains(m3u8Url, "m3u8") || m3u8Url == "" {
		flag.Usage()
		return
	}

	var downloadDir string
	pwd, _ := os.Getwd()
	//pwd = "/Users/chao/Desktop" //自定义地址
	downloadDir = pwd + "/movie/" + movieDir
	if isExist, _ := PathExists(downloadDir); !isExist {
		os.MkdirAll(downloadDir, os.ModePerm)
	} else {
		//download_dir = pwd + "/movie/" + movieDir + time.Now().Format("0601021504")
		//os.MkdirAll(download_dir, os.ModePerm)
	}

	m3u8Host := getHost(m3u8Url, hostType)
	m3u8Body := getM3u8Body(m3u8Url)
	//m3u8Body := getFromFile()

	tsKey := getM3u8Key(m3u8Host, m3u8Body)
	if tsKey != "" {
		fmt.Printf("待解密ts文件key: %s \n", tsKey)
	}

	ts_list := getTsList(m3u8Host, m3u8Body)
	fmt.Println("待下载ts文件数量:", len(ts_list))

	//下载ts
	downloader(ts_list, maxGoroutines, downloadDir, tsKey)

	switch runtime.GOOS {
	case "windows":
		winMergeFile(downloadDir)
	default:
		unixMergeFile(downloadDir)
	}
	os.Rename(downloadDir+"/merge.mp4", downloadDir+".mp4")
	os.RemoveAll(downloadDir)

	DrawProgressBar("Merging", float32(1), ProgressWidth, "merge.ts")
	fmt.Printf("\nDone! 耗时:%6.2fs\n", time.Now().Sub(now).Seconds())
}

//获取m3u8地址的host
func getHost(Url, ht string) (host string) {
	u, err := url.Parse(Url)
	checkErr(err)
	switch ht {
	case "apiv1":
		host = u.Scheme + "://" + u.Host + path.Dir(u.Path)
	case "apiv2":
		host = u.Scheme + "://" + u.Host
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
//TODO: 把 key 的 string 换成 bytes，防止有 0x00 存在的时候把 string 截断
func getM3u8Key(host, html string) (key string) {
	lines := strings.Split(html, "\n")
	key = ""
	for _, line := range lines {
		if strings.Contains(line, "#EXT-X-KEY") {
			uriPos := strings.Index(line, "URI")
			quotationMarkPos := strings.LastIndex(line, "\"")
			keyUrl := strings.Split(line[uriPos:quotationMarkPos], "\"")[1]
			if !strings.Contains(line, "http") {
				keyUrl = fmt.Sprintf("%s/%s", host, keyUrl)
			}
			res, err := grequests.Get(keyUrl, ro)
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
//modify: 2020-08-13 修复ts格式SyncByte合并不能播放问题
func downloadTsFile(ts TsInfo, downloadDir, key string, retries int) {
	// defer func() {
	// 	if r := recover(); r != nil {
	// 		fmt.Printf("网络不稳定，正在进行断点持续下载 %v\n", r)
	// 		downloadTsFile(ts, download_dir, key, retries-1)
	// 	}
	// }()

	if retries <= 0 {
		logger.Fatalln("已达到最大重试次数，任务失败")
		return
	}

	currPath := fmt.Sprintf("%s/%s", downloadDir, ts.Name)
	if isExist, _ := PathExists(currPath); isExist {
		//logger.Println("[warn] File: " + ts.Name + "already exist")
		return
	}
	res, err := grequests.Get(ts.Url, ro)
	if err != nil || !res.Ok {
		logger.Printf("下载失败，正在重试 %v\n", err)
		downloadTsFile(ts, downloadDir, key, retries-1)
		return
	}

	var origData []byte
	origData = res.Bytes() // res.Error 可能会更新，在这里检查一下是否接收到了来自服务器的所有 bytes
	if res.Error != nil {
		logger.Printf("下载失败，正在重试 %v\n", res.Error)
		downloadTsFile(ts, downloadDir, key, retries-1)
		return
	}

	if len(origData) == 0 {
		logger.Printf("返回空数据，正在重试\n")
		downloadTsFile(ts, downloadDir, key, retries-1)
		return
	}

	if key != "" {
		//若加密，解密ts文件 aes 128 cbc pack5
		origData, err = AesDecrypt(origData, []byte(key))
		if err != nil {
			logger.Printf("AES解密失败，正在重试 %v\n", err)
			downloadTsFile(ts, downloadDir, key, retries-1)
			return
		}
	}

	// https://en.wikipedia.org/wiki/MPEG_transport_stream
	// Some TS files do not start with SyncByte 0x47, they can not be played after merging,
	// Need to remove the bytes before the SyncByte 0x47(71).
	syncByte := uint8(71) //0x47
	bLen := len(origData)
	for j := 0; j < bLen; j++ {
		if origData[j] == syncByte {
			origData = origData[j:]
			break
		}
	}
	ioutil.WriteFile(currPath, origData, 0666)
}

//downloader m3u8下载器
func downloader(tsList []TsInfo, maxGoroutines int, downloadDir string, key string) {
	retry := 5 //单个ts下载重试次数
	var wg sync.WaitGroup
	limiter := make(chan struct{}, maxGoroutines) //chan struct 内存占用0 bool占用1
	tsLen := len(tsList)
	downloadCount := 0

	for _, ts := range tsList {
		wg.Add(1)
		limiter <- struct{}{}
		go func(ts TsInfo, downloadDir, key string, retryies int) {
			defer func() {
				wg.Done()
				<-limiter
			}()
			downloadTsFile(ts, downloadDir, key, retryies)
			downloadCount++
			DrawProgressBar("Downloading", float32(downloadCount)/float32(tsLen), ProgressWidth, ts.Name)
			return
		}(ts, downloadDir, key, retry)
	}
	wg.Wait()
}

// 进度条
func DrawProgressBar(prefix string, proportion float32, width int, suffix ...string) {
	pos := int(proportion * float32(width))
	s := fmt.Sprintf("[%s] %s%*s %6.2f%% \t%s",
		prefix, strings.Repeat("■", pos), width-pos, "", proportion*100, strings.Join(suffix, ""))
	fmt.Print("\r" + s)
}

// ============================== shell相关 ==============================

// 执行shell
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
func winMergeFile(path string) {
	os.Chdir(path)
	ExecWinShell("copy /b *.ts merge.tmp")
	ExecWinShell("del /Q *.ts")
	os.Rename("merge.tmp", "merge.mp4")
}

//unix合并文件
func unixMergeFile(path string) {
	os.Chdir(path)
	//cmd := `ls  *.ts |sort -t "\." -k 1 -n |awk '{print $0}' |xargs -n 1 -I {} bash -c "cat {} >> new.tmp"`
	cmd := `cat *.ts >> merge.tmp`
	ExecUnixShell(cmd)
	ExecUnixShell("rm -rf *.ts")
	os.Rename("merge.tmp", "merge.mp4")
}

// ============================== 加解密相关 ==============================

func PKCS7Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

func PKCS7UnPadding(origData []byte) []byte {
	length := len(origData)
	if length == 0 {
		return origData
	}
	unpadding := int(origData[length-1])
	return origData[:(length - unpadding)]
}

func AesEncrypt(origData, key []byte, ivs ...[]byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	var iv []byte
	if len(ivs) == 0 {
		iv = key
	} else {
		iv = ivs[0]
	}
	origData = PKCS7Padding(origData, blockSize)
	blockMode := cipher.NewCBCEncrypter(block, iv[:blockSize])
	crypted := make([]byte, len(origData))
	blockMode.CryptBlocks(crypted, origData)
	return crypted, nil
}

func AesDecrypt(crypted, key []byte, ivs ...[]byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	var iv []byte
	if len(ivs) == 0 {
		iv = key
	} else {
		iv = ivs[0]
	}
	blockMode := cipher.NewCBCDecrypter(block, iv[:blockSize])
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
