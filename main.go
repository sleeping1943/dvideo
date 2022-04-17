package main

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"dvideo/progress"

	"github.com/PuerkitoBio/goquery"
	"github.com/fatih/color"
	"github.com/gin-gonic/gin"
)

// 简书m3u8格式解析讲解:https://www.jianshu.com/p/e97f6555a070

// JSONConf 配置文件解析
type JSONConf struct {
	FilePath string `json:"filePath"`
}

// VideoInfo 视频信息
type VideoInfo struct {
	TotalCount   int64  `json:"totalCount"`
	CurrentIndex int64  `json:"currentIndex"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	TotalSize    int64  `json:"totalSize"`
	EncryptFlag  bool   `json:"EncryptFlag"`
	Syn          string `json:"Syn"`
}

// FileNameInfo 文件标题信息
type FileNameInfo struct {
	Name string
	URL  string
}

// StatesList 下载状态信息slice
type StatesList []VideoInfo

func (sl StatesList) Len() int {
	return len(sl)
}

func (sl StatesList) Swap(i, j int) {
	sl[i], sl[j] = sl[j], sl[i]
}

func (sl StatesList) Less(i, j int) bool {
	return sl[i].Name <= sl[j].Name
}

var conf JSONConf
var state chan VideoInfo
var states map[string]VideoInfo

var cfileName chan FileNameInfo
var fileNames map[string]FileNameInfo

var cfileName2 chan struct{}

// DownloadState 实时显示下载进度
func DownloadState() {
	for {
		select {
		case w := <-state:
			//fmt.Printf("%v\n", w)
			if w.CurrentIndex == w.TotalCount-1 {
				delete(states, w.Name)
			} else {
				states[w.Name] = w
			}
		case fileNameInfo := <-cfileName:
			fileNames[fileNameInfo.URL] = fileNameInfo
			cfileName2 <- struct{}{}
		default:
			time.Sleep(time.Duration(500 * time.Millisecond))
			//fmt.Println("waiting for chan...")
		}
	}
}

func init() {
	f, err := os.Open("./conf/conf.json")
	if err != nil {
		fmt.Println("init error:", err.Error())
		panic("init error")
	}
	byteConf, err := ioutil.ReadAll(f)
	if err != nil {
		fmt.Println("init error:", err.Error())
		panic("init error")
	}
	err = json.Unmarshal(byteConf, &conf)
	if err != nil {
		fmt.Println("init error:", err.Error())
		panic("init error")
	}
	fmt.Println("json conf:", conf)
}

// GetBaseURL : 从url中提取主网页地址
func GetBaseURL(url string) (baseURL string) {
	compile := regexp.MustCompile("https?://\\w+\\.\\w+\\.\\w+")
	submatch := compile.FindAllSubmatch([]byte(url), -1)
	//fmt.Println("submatch:", submatch)
	if len(submatch) <= 0 {
		compile = regexp.MustCompile("https?://\\w+\\.\\w+")
		submatch = compile.FindAllSubmatch([]byte(url), -1)
	}
	for _, m := range submatch {
		baseURL = string(m[0])
		fmt.Println("iframe基本地址:", string(m[0]))
		break
	}
	return baseURL
}

// ExtractInfo : 从输入的url下载原网页并从中抽取出标题和m3u8文件地址
func ExtractInfo(url string) (title string, m3u8URL string) {
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Get URL[%s] error:%s\n", url, err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Get URL[%s] body error:%s\n", url, err.Error())
		return
	}
	//fmt.Printf("HTML:%s\n", body)
	dom, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		fmt.Printf("创建dom树失败,err:%s\n", err.Error())
		return
	}
	nodeWorks := dom.Find("div[id=works]")
	title = nodeWorks.Find("h1").Text()
	iframeURL, isExists := nodeWorks.Find("iframe").Attr("src")
	if !isExists {
		fmt.Println("iframe不存在src属性")
		return
	}
	baseURL := GetBaseURL(url)
	resp, err = http.Get(baseURL + iframeURL)
	if err != nil {
		fmt.Println("获取iframe错误,err:", err.Error())
		return
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	index := 0
	for {
		byteLine, _, err := reader.ReadLine()
		if err == io.EOF {
			break
		}
		index++
		strLine := string(byteLine)
		// 提取最终的m3u8URL地址
		if strings.HasPrefix(strLine, "window.parent.document.getElementById(\"download\").innerHTML") {
			compile := regexp.MustCompile("[a-zA-z]+://[^\\s*]+.m3u8")
			submatch := compile.FindAllSubmatch([]byte(strLine), -1)
			if len(submatch) <= 0 {
				compile = regexp.MustCompile("[a-zA-z]+://[^\\s*]+.m3u8")
				submatch = compile.FindAllSubmatch([]byte(strLine), -1)
			}
			for _, m := range submatch {
				m3u8URL = string(m[0])
				fmt.Println("提取到的m3u8地址:", string(m[0]))
				break
			}
		}
	}
	fmt.Printf("标题:%s\n最终的m3u8地址:%s\n", title, m3u8URL)
	return title, m3u8URL
}

func downloadM3u8File(url string) (m3u8 string, baseURL string, isRedirect bool, err error) {
	//1、下载指定url的m3u8文件
	//fmt.Println("url:", url)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("http get error:", err.Error())
		return "", "", isRedirect, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("ioutil.ReadAll err:", err.Error())
		return "", "", isRedirect, err
	}
	baseM3u8Content := string(body)
	//fmt.Println("baseM3u8Content:", baseM3u8Content)
	//fmt.Println("url:", url)
	//2、正则提取网页的基地址
	baseURL = GetBaseURL(url)
	//3、提取出baseM3u8Content中的二级相对m3u8地址
	sections := strings.Split(baseM3u8Content, "\n")
	//fmt.Println("sections:", sections, "baseUrl:", baseURL)
	nextIsM3u8Url := false
	var theTrueM3u8Url string
	for _, value := range sections {
		//fmt.Println("value:", value)
		//if len(value) <= 0 || value = "" {
		//	fmt.Println("this value is null, skip!!")
		//	continue
		//}
		if nextIsM3u8Url {
			theTrueM3u8Url = baseURL + value
			break
		}
		if strings.HasPrefix(value, "#EXT-X-STREAM-INF") {
			nextIsM3u8Url = true
			isRedirect = true
		}
	}
	// 如果没有在m3u8文件中定位到跳转后的二级地址，用输入的地址下载
	if len(theTrueM3u8Url) == 0 {
		theTrueM3u8Url = url
	}
	fmt.Println("二级m3u8文件跳转地址[theTrueM3u8Url]:", theTrueM3u8Url)
	//4、下载最终的m3u8文件
	resp2, err := http.Get(theTrueM3u8Url)
	if err != nil {
		fmt.Println("http get last m3u8 error:", err.Error())
		return "", "", isRedirect, err
	}
	defer resp2.Body.Close()
	body2, err := ioutil.ReadAll(resp2.Body)
	if err != nil {
		fmt.Println("ioutil.ReadAll err:", err.Error())
		return "", "", isRedirect, err
	}
	//strings.Split(string(body2), "\n")
	//fmt.Println("body2:", string(body2))

	return string(body2), baseURL, isRedirect, nil
}

// DownloadTsFile 下载单个的ts文件
func DownloadTsFile(isDownload bool, baseURL, url string) ([]byte, error) {
	var tsFilePath = baseURL + url
	//fmt.Println("----------------------tsFilePath:", tsFilePath)
	resp, err := http.Get(tsFilePath)
	if err != nil {
		return []byte(""), err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return []byte(""), err
	}
	if isDownload {
		sections := strings.Split(url, "/")
		f, err := os.Create(sections[len(sections)-1])
		if err != nil {
			fmt.Println("os.Create error:", err.Error())
			return []byte(""), errors.New("1")
		}
		defer f.Close()
		_, err = f.Write(body)
		if err != nil {
			log.Println("writeFile error ..err =", err)
			return []byte(""), errors.New("2")
		}
	}
	return body, nil
}

// ParseM3u8File 解析m3u8中的section
func ParseM3u8File(m3u8Content string) (sections []string, totalTime float64, key string) {
	//fmt.Println("m3u8Content:", m3u8Content)
	fileNames := strings.Split(m3u8Content, "\n")
	//fmt.Println("fileNames:", fileNames)
	isNextTsSectoin := false
	var tsTimeArr []float64
	for index, fileName := range fileNames {
		//fmt.Println("fileName:", fileName)
		index++
		if isNextTsSectoin {
			sections = append(sections, fileName)
		}
		if strings.HasPrefix(fileName, "#EXTINF") {
			fields := strings.Split(fileName, ":")
			if len(fields) > 0 {
				strDuration := fields[len(fields)-1]
				strDuration = strings.Trim(strDuration, ": !,.?")
				fDuration, err := strconv.ParseFloat(strDuration, 64)
				if err != nil {
					color.Red("解析Ts文件时长出错:", err.Error())
				} else {
					tsTimeArr = append(tsTimeArr, fDuration)
				}
			}
			isNextTsSectoin = true
		} else {
			isNextTsSectoin = false
		}
		// 提取key的相对路径
		if strings.HasPrefix(fileName, "#EXT-X-KEY") {
			sectionKeys := strings.Split(fileName, ",")
			for _, value := range sectionKeys {
				if strings.HasPrefix(value, "URI") {
					URISections := strings.Split(value, "=\"")
					fmt.Println("获取解密key的相对地址:", URISections)
					for _, elem := range URISections {
						if elem != "URI" {
							key = elem
							key = strings.TrimRight(key, "\"")
							//fmt.Println("key_uri:", key)
						}
					}
					break
				}
			}
			//fmt.Println("sectionKeys---------------------:", sectionKeys)
		}
	}
	if len(tsTimeArr) > 0 {
		for _, value := range tsTimeArr {
			totalTime += value
		}
	}
	return sections, totalTime, key
}

func unpad(src []byte) ([]byte, error) {
	length := len(src)
	unpadding := int(src[length-1])
	if unpadding > length {
		return nil, errors.New("unpad error")
	}
	return src[:(length - unpadding)], nil
}

// DecryTsFile 解密
func DecryTsFile(key, content string) (string, error) {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		fmt.Println("DecryTsFile error:", err.Error())
		return "", err
	}
	//decodeMsg, _ := hex.DecodeString(content)
	iv := make([]byte, aes.BlockSize)
	msg := []byte(content)
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(msg, msg)
	unpadMsg, err := unpad(msg)
	if err != nil {
		return "", err
	}
	return string(unpadMsg), nil
}

// 下载单个视频
func downloadVideo(beginTime time.Time, isDownloadTmpTsFile bool, baseURL, key, title, filePath string, fileNames []string) {
	var contents [][]byte
	totalCount := len(fileNames)
	if len(key) > 0 {
		keyURI := baseURL + key
		fmt.Println("解密Ts文件的秘钥key全URL:", keyURI)
		resp, err := http.Get(keyURI)
		if err != nil {
			fmt.Println("Get key error:", err.Error())
			return
		}
		defer resp.Body.Close()
		keyBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Println("key ioutil.ReadAll err:", err.Error())
			return
		}
		key = string(keyBody)
		color.HiGreen("获取解密Ts文件的key成功!秘钥内容:%s", key)
	}
	var downloadBar progress.Bar
	var barGraph = "█"
	var encryptFlag = false
	if len(key) > 0 {
		barGraph = "*"
		encryptFlag = true
	}
	downloadBar.NewOptionWithGraph(0, int64(totalCount), barGraph)
	// 开始下载之前存入一个，不然1个都没下载成功时，查询为null
	state <- VideoInfo{
		Name:         title,
		URL:          "",
		TotalCount:   int64(totalCount),
		CurrentIndex: int64(0),
		EncryptFlag:  encryptFlag,
		Syn:          base64.StdEncoding.EncodeToString([]byte(title)),
	}
	for index, fileName := range fileNames {
		//downloadBar.Play(int64(index+1), beginTime)
		byteContent, err := DownloadTsFile(isDownloadTmpTsFile, baseURL, fileName)
		//fmt.Println("共", totalCount, "个ts文件 现在下载到第", index+1, "个")
		if err != nil {
			fmt.Println("DownloadTsFile error:", err.Error())
			return
		}
		//fmt.Println("\nBefore state<-...")
		state <- VideoInfo{
			Name:         title,
			URL:          baseURL + fileName,
			TotalCount:   int64(totalCount),
			CurrentIndex: int64(index),
			EncryptFlag:  encryptFlag,
			Syn:          base64.StdEncoding.EncodeToString([]byte(title)),
		}
		//fmt.Println("After state<-...")
		stringContent := string(byteContent)
		if len(key) > 0 {
			stringContent, err = DecryTsFile(key, string(byteContent))
			//fmt.Println("共", totalCount, "个ts文件 现在解密到第", index+1, "个")
			if err != nil {
				fmt.Println("DecryTsFile error:", err.Error())
				return
			}
		}
		contents = append(contents, []byte(stringContent))
	}
	downloadBar.Finish()
	video := bytes.Join(contents, []byte(""))
	f, err := os.Create(filePath)
	if err != nil {
		fmt.Println("os.Create error:", err.Error())
		return
	}
	defer f.Close()
	_, err = f.Write(video)
	if err != nil {
		log.Println("writeFile error ..err =", err)
		return
	}
	return
}

func downloadVideoDirect(url, filePath string) {
	resp, err := http.Get(string(url))
	if err != nil {
		fmt.Println("Get video file error:", err.Error())
		return
	}
	defer resp.Body.Close()
	fileBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("fileBody ioutil.ReadAll err:", err.Error())
		return
	}
	f, err := os.Create(filePath)
	if err != nil {
		fmt.Println("os.Create error:", err.Error())
		return
	}
	defer f.Close()
	_, err = f.Write(fileBody)
	if err != nil {
		log.Println("writeFile error ..err =", err)
		return
	}
	return
}

func onDownloadVideo(url string) {
	title, m3u8URL := ExtractInfo(string(url))
	cfileName <- FileNameInfo{
		URL:  url,
		Name: title,
	}
	//fmt.Println("")
	filePath := fmt.Sprintf("%s/%s.mp4", conf.FilePath, string(title))
	// 若是m3u8文件则按流程下载,其余文件直接下载
	if strings.HasSuffix(string(m3u8URL), ".m3u8") || strings.HasSuffix(string(m3u8URL), ".M3U8") {
		color.HiYellow("下载m3u8文件")
		/*
			有的m3u8文件有跳转，需要解析输入的m3u8文件再次下载新的m3u8文件,
			如果不续跳转,则使用输入的url指向的m3u8文件进行解析
		*/
		m3u8Content, baseURL, isRedirect, err := downloadM3u8File(string(m3u8URL))
		if !isRedirect {
			baseURL = string(m3u8URL)
			index := strings.LastIndex(baseURL, "/")
			if index != -1 {
				baseURL = baseURL[:index+1]
			}
			color.HiBlue("new baseURL:", baseURL)

		}
		if err != nil {
			color.Red("downloadM3u8File error:", err.Error())
			return
		}
		color.HiYellow("解析m3u8文件")
		fileNames, totalTime, key := ParseM3u8File(m3u8Content)
		//fmt.Printf("m3u8Content:%v\n", m3u8Content)
		//fmt.Println("fileName:", fileNames)
		//fmt.Printf("fileNames:%v\n key:%v\n", fileNames, key)
		color.HiYellow("下载视频文件")
		beginTime := time.Now()
		seconds := int64(totalTime)
		color.Green("开始下载时间:%s 视频总时间:%02dh%02dm%02ds [*表示ts文件加密,█表示ts文件未加密]:\n", time.Now().Format("2006-01-02 15:04:05"), seconds/3600, seconds/60%60, seconds%60)
		downloadVideo(beginTime, false, baseURL, key, title, filePath, fileNames)
		color.Red("结束下载时间:%s\n文件下载路径:%s\n共使用时间:%s:\n", time.Now().Format("2006-01-02 15:04:05"), filePath, time.Since(beginTime).String())
	} else {
		color.HiYellow("直接下载视频文件")
		beginTime := time.Now()
		color.Green("开始下载时间:%s[*表示ts文件加密,█表示ts文件未加密]:\n", time.Now().Format("2006-01-02 15:04:05"))
		downloadVideoDirect(string(m3u8URL), filePath)
		color.Red("结束下载时间:%s\n文件下载路径:%s\n共使用时间:%s:\n", time.Now().Format("2006-01-02 15:04:05"), filePath, time.Since(beginTime).String())
	}
}

func main() {
	router := gin.Default()
	router.StaticFile("/", "./static/index.html")
	router.StaticFS("imgs", http.Dir("./imgs"))
	router.POST("/download", func(c *gin.Context) {
		fmt.Println(c.FullPath())
		url := c.Query("url")
		go func(url string) {
			onDownloadVideo(url)
		}(url)
		<-cfileName2
		url = fmt.Sprintf("正在下载%s,网址:%s\n", fileNames[url].Name, url)
		c.JSON(200, gin.H{
			"msg":    "Success",
			"detail": url,
		})
	})
	router.GET("/status", func(c *gin.Context) {
		//retBytes, err := json.Marshal(states)
		//if err != nil {
		//	c.JSON(502, gin.H{
		//		"code":   502,
		//		"msg":    "ERROR",
		//		"detail": "other",
		//	})
		//} else {
		var statesList StatesList
		for _, value := range states {
			statesList = append(statesList, value)
		}
		sort.Sort(statesList)
		c.JSON(200, gin.H{
			"code":   200,
			"msg":    "Success",
			"detail": statesList,
		})
		//}
	})
	state = make(chan VideoInfo)
	states = make(map[string]VideoInfo)

	cfileName = make(chan FileNameInfo)
	cfileName2 = make(chan struct{})
	fileNames = make(map[string]FileNameInfo)
	go DownloadState()
	router.Run(":5277")
}
