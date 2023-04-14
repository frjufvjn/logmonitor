package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ashwanthkumar/slack-go-webhook"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/jasonlvhit/gocron"
	cmap "github.com/orcaman/concurrent-map/v2"
	"github.com/tidwall/pretty"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
	"gopkg.in/yaml.v2"
)

// config
type Config struct {
	configInfo ConfigInfo
}

// config getter
func (c Config) ConfigInfo() ConfigInfo {
	return c.configInfo
}

// config setter
func (c *Config) SetConfigInfo(configInfo ConfigInfo) {
	c.configInfo = configInfo
}

// ConfigInfo from logmonitor-config.yml
type ConfigInfo struct {
	AppName                  string     `yaml:"appName"`
	AwsEc2                   bool       `yaml:"awsEc2"`
	RunCycleSec              uint64     `yaml:"runCycleSec"`
	SameKeywordThreasholdSec uint64     `yaml:"sameKeywordThreasholdSec"`
	SameKeywordExtractLen    int        `yaml:"sameKeywordExtractLen"`
	Alarm                    Alarm      `yaml:"alarm"`
	File                     []FileList `yaml:"filelist"`
}

// Alarm
type Alarm struct {
	Jandi Jandi `yaml:"jandi"`
	Slack Slack `yaml:"slack"`
}

// Jandi
type Jandi struct {
	Enable              bool   `yaml:"enable"`
	IncommingWebhookUrl string `yaml:"incommingWebhookUrl"`
}

// Slack
type Slack struct {
	Enable              bool   `yaml:"enable"`
	IncommingWebhookUrl string `yaml:"incommingWebhookUrl"`
	Username            string `yaml:"username"`
	Channel             string `yaml:"channel"`
	IconEmoji           string `yaml:"iconemoji"`
}

// FileList is to scan file information
type FileList struct {
	ID       string     `yaml:"id"`
	Path     string     `yaml:"path"`
	Keywords []Keywords `yaml:"keywords"`
}

// Keywords is scan properties
type Keywords struct {
	Patern string `yaml:"pattern"`
}

// 잔디 웹훅 api request
type JandiRequest struct {
	Body         string             `json:"body"`
	ConnectColor string             `json:"connectColor"`
	ConnectInfo  []JandiConnectInfo `json:"connectInfo"`
}

// 잔디 웹훅 api request connctInfo
type JandiConnectInfo struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	ImageURL    string `json:"imageUrl,omitempty"`
}

// 읽어들인 라인 포인트
type MapLatestReadPoint struct {
	line int64
}

// 키워드 감지되었을때 시간 정보
type MapOfDetectedTime struct {
	time time.Time
}

// 처음 프로그램 시작시 알람을 보내지 않도록 설정
type MapOfIsBoot struct {
	boot bool
}

const programName = "log-monitor"

var (
	appVersion               string
	buildTime                string
	gitCommit                string
	gitRef                   string
	config                   Config
	logn                     = log.Println
	logf                     = log.Printf
	taskMap                  cmap.ConcurrentMap[MapLatestReadPoint] // map[string]int64
	taskTimeMap              cmap.ConcurrentMap[MapOfDetectedTime]  // map[string]time.Time
	isBootMap                cmap.ConcurrentMap[MapOfIsBoot]
	exeCount                 uint64 = 0
	sameKeywordThreasholdSec uint64 = 30
	sameKeywordExtractLen    int    = 20
	awsInstanceId            string = ""
	awsRegion                string = ""
	appName                  string = ""
	isConsoleMode                   = flag.Bool("console", false, "콘솔모드 사용여부 true이면 file logger를 사용하지 않음.")
)

// 초기화 수행 flag parse
func init() {
	flag.Parse()
}

// main method
func main() {

	if isFlagInputed("jandi") {
		// alarmSend("테스트", "테스트", *jandiTestUrl)
		return
	}

	if isFlagInputed("slack") {
		return
	}

	setupLogger()

	if configInfo, err := setupConfigYaml(); err == nil {

		// set config
		config.SetConfigInfo(configInfo)

		// config variables set
		runCycle := configInfo.RunCycleSec
		sameKeywordThreasholdSec = configInfo.SameKeywordThreasholdSec
		sameKeywordExtractLen = configInfo.SameKeywordExtractLen
		appName = configInfo.AppName

		configTrace, _ := json.Marshal(configInfo)
		logn("config trace :: ", string(pretty.Pretty(configTrace)))

		// program manipulate map
		taskMap = cmap.New[MapLatestReadPoint]()    // make(map[string]int64)
		taskTimeMap = cmap.New[MapOfDetectedTime]() // make(map[string]time.Time)
		isBootMap = cmap.New[MapOfIsBoot]()         // make(map[string]bool)

		// aws metadata
		if configInfo.AwsEc2 {
			metadata := ec2metadata.New(session.New())
			m, err := metadata.GetInstanceIdentityDocument()
			check(err)

			logn("AWS metadata: instance id:", m.InstanceID, "region:", m.Region)
			awsInstanceId = m.InstanceID
			awsRegion = m.Region
		}

		// os signal receive and after process
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			for {
				s := <-sigs
				logn("signal received:", s.String())
				if s.String() == "terminated" {
					logn("normal exit signal received. then after process~~")
					alarmSendIfExit("log-monitor-bot가 정상 종료되었습니다. bye bye")
				}
				os.Exit(0)
			}
		}()

		// scheduler
		gocron.Every(runCycle).Second().Do(task, &configInfo)
		<-gocron.Start()

	} else {
		log.Fatalln("yaml config setup failed:", err)
	}
}

// 스케줄러 task 관리
func task(conf *ConfigInfo) {

	files := conf.File
	for x := range files {
		taskID := files[x].ID
		g, _ := taskMap.Get(taskID)
		beforeSeek := g.line // taskMap[taskID]

		afterSeek, _ := logScan(beforeSeek, files[x], taskID)
		s := MapLatestReadPoint{afterSeek}
		taskMap.Set(taskID, s)
		isBootMap.Set(taskID, MapOfIsBoot{boot: true})
		// taskMap[taskID] = afterSeek
	}
}

// 로그파일을 이전에 읽었던 위치부터 파일의 끝까지 읽고 검출할 패턴이 발견될 경우 현재 사이즈와 함께 반환한다.
func logScan(seekPoint int64, fileInfo FileList, taskID string) (int64, map[int]string) {

	// 종종 파일 내용에 대해 더 많은 제어를 하고 싶을때가 있습니다. 이를 위해선 파일을 Open하여 os.File 값을 얻습니다.
	f, err := os.Open(fileInfo.Path)
	if err != nil {
		logn(err)
		return 0, nil
	}
	defer f.Close()

	// 파일의 사이즈 구하기
	stat, err := os.Stat(fileInfo.Path)
	check(err)
	size := stat.Size()
	logf("- Task ID: %s, File Size: %d, seekPoint: %d\n", fileInfo.ID, size, seekPoint)

	var findPoint int64
	if seekPoint != 0 {
		findPoint = seekPoint
	}

	// 패턴이 발견되면 패턴 맵을 리턴한다.
	result := make(map[int]string)

	// 최초 스케줄 실행시 모든 파일을 읽되 알람발송은 skip하도록한다.
	previousExeCount := atomic.LoadUint64(&exeCount)

	// 이전과 비교해서 새로운 로그가 쌓였다면
	if previousExeCount > 0 && size > findPoint {

		// 또한 파일의 특정 위치를 Seek하고 그 지점으로부터 Read를 할 수도 있습니다.
		seekOffset, err := f.Seek(findPoint, 0)
		check(err)

		readByteSize := size - findPoint
		readByte := make([]byte, readByteSize)
		addSize, err := f.Read(readByte)
		check(err)

		logf("- Task ID: %s, Seek: %d bytes @ %d\n", fileInfo.ID, addSize, seekOffset)

		byteSlice := bytes.Split(readByte, []byte("\n"))

		// before seek point로 부터 끝까지 읽어들인 바이트에서 라인별로 분할
		for _, line := range byteSlice {

			// 읽어들인 라인에서 설정된 키워드와 일치할때
			keywords := fileInfo.Keywords
			for x := range keywords {

				isSend := false
				findKeyword := keywords[x].Patern

				if isIncludeKeyword(line, findKeyword) {

					extractKey := getDetectedSameKeyword(line)
					isSend = isAlarmSend(extractKey, taskID)
					taskTimeMap.Set(extractKey, MapOfDetectedTime{time.Now()})

					if isSend {
						go alarmSendWrapper(string(line), findKeyword, taskID)
					}

					result[x] = findKeyword
				}
			}
		}
	}

	if previousExeCount == 0 {
		atomic.AddUint64(&exeCount, 1)
	}

	return size, result
}

func isIncludeKeyword(line []byte, findKeyword string) bool {
	if bytes.Contains(line, []byte(findKeyword)) {
		logn(">> - 검출 키워드:", findKeyword, "검출된 라인:", string(line))
		return true
	}
	return false
}

// NOT USE
// func isFormattedLog(line []byte) bool {
// 	if bytes.Contains(line, []byte("]")) {
// 		return true
// 	}
// 	return false
// }

func isAlarmSend(extractKey string, taskID string) bool {
	b, _ := isBootMap.Get(taskID)
	if !b.boot {
		logn("if boot time detect, alarm send skip.")
		return false
	}

	prev, _ := taskTimeMap.Get(extractKey)
	diff := time.Since(prev.time)

	if diff.Seconds() > float64(sameKeywordThreasholdSec) {
		logn(">> 전송: sameKeywordThreashold over~~ Alarm Send!!")
		return true
	} else {
		logn(">> 미전송: sameKeywordThreashold not over~~ Alarm No Send!!")
		return false
	}
}

func getDetectedSameKeyword(line []byte) string {
	extractStr := string(line)
	if len(extractStr) > sameKeywordExtractLen {
		return extractStr[:sameKeywordExtractLen]
	}
	return extractStr
}

// 파일을 읽으려면 대부분의 에러 호출을 확인해야합니다. 이 헬퍼는 아래에서의 에러 체크를 간소화합니다.
func check(e error) {
	if e != nil {
		logn(e)
	}
}

func alarmSendWrapper(logData string, keyword string, taskID string) {
	go slackSend(logData, keyword, taskID)
	go jandiSned(logData, keyword, taskID)
}

func alarmSendIfExit(msg string) {
	slackSend(msg, "NONE", "ALL")
	jandiSned(msg, "NONE", "ALL")
}

func slackSend(logData string, keyword string, taskID string) {
	if !config.ConfigInfo().Alarm.Slack.Enable {
		return
	}

	logn(">> slack send")

	webhookUrl := config.ConfigInfo().Alarm.Slack.IncommingWebhookUrl
	att := slack.Attachment{}
	att.AddField(slack.Field{Title: "Task", Value: taskID})
	att.AddField(slack.Field{Title: "Detect", Value: keyword})

	payload := slack.Payload{
		Text:        logData,
		Username:    config.ConfigInfo().Alarm.Slack.Username,
		Channel:     config.ConfigInfo().Alarm.Slack.Channel,
		IconEmoji:   config.ConfigInfo().Alarm.Slack.IconEmoji,
		Attachments: []slack.Attachment{att},
	}
	err := slack.Send(webhookUrl, "", payload)
	if len(err) > 0 {
		fmt.Printf("error: %s\n", err)
	}
}

func jandiSned(logData string, keyword string, taskID string) {

	if !config.ConfigInfo().Alarm.Jandi.Enable {
		return
	}

	logn(">> jandi send")

	// jandiUrl := jandi.IncommingWebhookUrl
	// instanceId := awsInstanceId
	// region := awsRegion

	// jandiReq := &JandiRequest{
	// 	Body:         logData,
	// 	ConnectColor: "#FAC11B",
	// 	ConnectInfo: []JandiConnectInfo{
	// 		{
	// 			Title:       "detected-keyword",
	// 			Description: keyword,
	// 		},
	// 		{
	// 			Title:       "instance-id",
	// 			Description: instanceId,
	// 		},
	// 		{
	// 			Title:       "region",
	// 			Description: region,
	// 		},
	// 		{
	// 			Title:       "app-desc",
	// 			Description: appName,
	// 		},
	// 	},
	// }

	// payloadJson, _ := json.Marshal(&jandiReq)

	// req, err := http.NewRequest("POST", jandiUrl, bytes.NewBuffer(payloadJson))
	// req.Header.Set("Content-type", "application/json")
	// req.Header.Set("X-Requested-With", "XMLHttpRequest")
	// req.Header.Set("Accept", "application/vnd.tosslab.jandi-v2+json")

	// client := &http.Client{
	// 	Timeout: 60 * time.Second,
	// }
	// res, err := client.Do(req)
	// if err != nil {
	// 	prn("[http request fail :", err, "]")
	// }
	// defer client.CloseIdleConnections()
	// defer res.Body.Close()

	// resBody, resErr := ioutil.ReadAll(res.Body)
	// if resErr != nil {
	// 	prn("http response error :", resErr)
	// 	return
	// }

	// if res.StatusCode == 200 {
	// 	responseData := string(resBody)
	// 	logn("jandi api send success -> response:", responseData)
	// } else {
	// 	logn("jandi api send fail response status code:", res.StatusCode)
	// }
}

func createDirIfNotExist(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			panic(err)
		}
	}
}

func setupLogger() {
	if *isConsoleMode {
		return
	}

	currPath, _ := os.Getwd()
	createDirIfNotExist(currPath + "/logs")

	l := &lumberjack.Logger{
		Filename:   currPath + "/logs/" + programName + ".log",
		MaxSize:    1, // megabytes
		MaxBackups: 5,
		MaxAge:     28,    //days
		Compress:   false, // disabled by default
	}
	log.SetOutput(l)
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)

	go func() {
		for {
			<-c
			l.Rotate()
		}
	}()
}

func setupConfigYaml() (ConfigInfo, error) {

	// config from yaml
	conf := ConfigInfo{}
	confFilename := "./logmonitor-config.yml"
	source, err1 := ioutil.ReadFile(confFilename)
	if err1 != nil {
		logn("read config error : ", err1)
		return conf, err1
	}

	err2 := yaml.Unmarshal(source, &conf)
	if err2 != nil {
		logn("parse config error: ", err2)
		return conf, err2
	}

	return conf, nil
}

// argument name에 해당하는 flag를 입력했는지 여부를 체크한다.
func isFlagInputed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
