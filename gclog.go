package gclog

//自动按照时间切分日志
//kill -USR1 动态提升日志级别
//kill -USR2 动态降低日志级别
//Verb 等接口直接输入对应前缀的日志，低于一定等级不进行输出

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	verbLog int = iota
	debugLog
	infoLog
	noticeLog
	warningLog
	errorLog = 5
)

var headName = []string{
	verbLog:    "[VERB] ",
	debugLog:   "[DEBUG] ",
	infoLog:    "[INFO] ",
	noticeLog:  "[NOTICE] ",
	warningLog: "[WARNING] ",
	errorLog:   "[ERROR] ",
}

var (
	isInitLogFile    bool          //是否已经初始化完毕
	writeToFile      bool          //是否写入文件，=false写入屏幕
	logFile          *os.File      //文件流
	logLevel         int           //日志级别
	fileName         string        //日志文件名
	levelLock        *sync.Mutex   //日志级别锁
	fileLock         *sync.Mutex   //文件锁，写入时锁住，防止切日志时空指针
	logSliceInterval time.Duration //日志切分的时间间隔
	logStorageTime   time.Duration //日志保存的时间
	logFileFlashTime time.Time     //上次文件流刷新的时间
)

func init() {
	writeToFile = false                 //默认不输出到文件
	logLevel = noticeLog                //默认notice级别
	logStorageTime = 7 * 24 * time.Hour //日志文件默认保存7日
	levelLock = new(sync.Mutex)
	fileLock = new(sync.Mutex)

	//启动信号量监听
	go signalListen()
	//启动日志定时切分、删除过期日志
	go logSliceByDate()
}

//InitLogFile 初始化日志文件
func InitLogFile(filename string) error {
	fileLock.Lock()
	defer fileLock.Unlock()
	//尝试打开文件
	var err error
	logFile, err = os.OpenFile(filename, os.O_APPEND+os.O_WRONLY, os.ModeAppend)
	if err != nil {
		//发现文件不存在，创建一个新的
		if os.IsNotExist(err) == true {
			var createErr error
			// fmt.Printf("file %s exist, open", filename)
			logFile, createErr = os.Create(filename)
			if createErr != nil {
				fmt.Printf("create file %s failed, bacauce %s", filename, createErr.Error())
				return createErr
			}
		} else {
			//非文件不存在error
			fmt.Printf("open file %s failed, bacauce %s", filename, err.Error())
			return err
		}
	}
	writeToFile = true
	fileName = filename
	logFileFlashTime = time.Now().Round(time.Hour)
	return nil
}

//SetLogSliceInterval 设置日志切分的时间间隔，不设置则默认为1 day
func SetLogSliceInterval(interval time.Duration) {
	logSliceInterval = interval
}

//SetLogStorageTime 设置日志保存的时间，不设置默认为7 day
func SetLogStorageTime(storageTime time.Duration) {
	if storageTime < 0 {
		logStorageTime = -1 * storageTime
	} else {
		logStorageTime = storageTime
	}
}

//logSliceByDate 根据时间对日志进行切片
func logSliceByDate() {
	for {
		//不写入文件，不需要切分
		if writeToFile == false {
			Verb("logFile close, exit slice log loop")
		} else if time.Now().After(logFileFlashTime.Add(logSliceInterval)) {
			//当前时间在上次刷新时间+日志切分间隔时间之后，需要切日志
			//清理过期日志
			deleteLogFile()
			//rename日志
			moveLogFile()
		}
		time.Sleep(30 * time.Second)
	}
}

//moveLogFile 将当前输出日志文件，根据时间变更名称
func moveLogFile() {
	//对logFile加锁，日志暂时输出到标准输出（防止失败后无输出情况）
	fileLock.Lock()
	writeToFile = false

	//获取日志目录、日志名称等信息
	dir, name, suffix := getFileInfo()
	timeNow := time.Now()
	//exp:"./test_2018_4_8_16.log"
	newName := fmt.Sprintf("%s/%s_%02d_%02d_%02d_%02d%s", dir, name, timeNow.Year(), timeNow.Month(), timeNow.Day(), timeNow.Hour(), suffix)

	logFile.Close()
	err := os.Rename(fileName, newName)
	if err != nil {
		Warning("rename file %s to %s failed, because %s", fileName, newName)
		//不跳出，继续Init使用旧的日志文件
	}
	//rename成功，初始化全新的日志文件，失败，使用旧的日志文件
	fileLock.Unlock()
	InitLogFile(fileName)
}

//deleteLogFile 清理过期日志
func deleteLogFile() {
	//删除操作不涉及logFile，因此不加锁
	//获取日志目录、日志名称等信息
	dir, name, suffix := getFileInfo()
	file, err := os.Open(dir)
	defer file.Close()
	if err != nil {
		Warning("try to delete file, open dir %s failed, because %s", dir, err.Error())
		return
	}

	//取日志目录下，所有文件
	fileNames, err := file.Readdir(0)
	if err != nil {
		Warning("try to delete file, read dir %s info failed, because %s", dir, err.Error())
		return
	}
	for _, v := range fileNames {
		//必须要包含name、后缀，创建时间在logStorageTime之前才能删除
		if strings.Contains(v.Name(), name) && strings.Contains(v.Name(), suffix) &&
			v.ModTime().Before(logFileFlashTime.Add(-1*logStorageTime)) {
			//防止极端情况下，删除正在写入的log文件
			if v.Name() == name+suffix {
				continue
			}

			//删除对应文件
			errRemove := os.Remove(dir + "/" + v.Name())
			if errRemove != nil {
				Warning("try to delete file, delete file name %s failed, because %s", dir+"/"+v.Name(), err.Error())
				continue
			} else {
				Notice("try to delete file, delete file name %s success", dir+"/"+v.Name())
			}
		}
	}
}

//getFileInfo 取当前日志名称的信息，返回:日志目录,日志名称,日志后缀
func getFileInfo() (string, string, string) {
	var (
		dir    string
		name   string
		suffix string
	)
	tablePoint := strings.LastIndex(fileName, "/")
	suffixPoint := strings.LastIndex(fileName, ".")
	//找不到“/”，默认选当前目录
	if tablePoint == -1 {
		dir = "./"
	} else {
		dir = fileName[:tablePoint]
	}

	//找不到后缀的"."，默认后缀为.log，名称取"/"后所有字符
	if suffixPoint == -1 {
		name = fileName[tablePoint+1:]
		suffix = ".log"
	} else {
		name = fileName[tablePoint+1 : suffixPoint]
		suffix = fileName[suffixPoint:]
	}

	return dir, name, suffix
}

//CloseFile 关闭文件流，继续打印改为输出到标准输出
func CloseFile() {
	fileLock.Lock()
	defer fileLock.Unlock()
	logFile.Close()
	writeToFile = false
}

//signalListen 监听日志级别改变事件
func signalListen() {
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGUSR1, syscall.SIGUSR2)
	defer signal.Stop(c)
	for {
		s := <-c
		Warning("recvice signal %s")
		if s == syscall.SIGUSR1 {
			LogLevelUp()
		} else if s == syscall.SIGUSR2 {
			LogLevelDown()
		}
	}
}

//LogLevelUp 提高日志级别
func LogLevelUp() {
	levelLock.Lock()
	defer levelLock.Unlock()
	if logLevel >= verbLog && logLevel < errorLog {
		logLevel++
		Warning("log level up")
	}
}

//LogLevelDown 降低日志级别
func LogLevelDown() {
	levelLock.Lock()
	defer levelLock.Unlock()
	if logLevel >= verbLog && logLevel < errorLog {
		logLevel--
		Warning("log level down")
	}
}

//SetLogLevel 设置日志级别
func SetLogLevel(level int) {
	levelLock.Lock()
	defer levelLock.Unlock()
	if level >= verbLog && level < errorLog {
		logLevel = level
	}
}

//Verb 输出verb日志
func Verb(msg string, v ...interface{}) {
	if logLevel >= verbLog {
		writeLog(headName[verbLog], fmt.Sprintf(msg, v...))
	}
}

//Debug 输出debug日志
func Debug(msg string, v ...interface{}) {
	if logLevel >= debugLog {
		writeLog(headName[debugLog], fmt.Sprintf(msg, v...))
	}
}

//Info 输出info日志
func Info(msg string, v ...interface{}) {
	if logLevel >= infoLog {
		writeLog(headName[infoLog], fmt.Sprintf(msg, v...))
	}
}

//Notice 输出notice日志
func Notice(msg string, v ...interface{}) {
	if logLevel >= noticeLog {
		writeLog(headName[noticeLog], fmt.Sprintf(msg, v...))
	}
}

//Warning 输出warning日志
func Warning(msg string, v ...interface{}) {
	if logLevel >= warningLog {
		writeLog(headName[warningLog], fmt.Sprintf(msg, v...))
	}
}

//Error 输出error日志
func Error(msg string, v ...interface{}) {
	if logLevel >= errorLog {
		writeLog(headName[errorLog], fmt.Sprintf(msg, v...))
	}
}

//writeLog 输出日志的方法
func writeLog(level string, msg string) {
	if writeToFile == true {
		fileLock.Lock()
		defer fileLock.Unlock()
		logger := log.New(logFile, level, log.LstdFlags+log.Lshortfile)
		logger.Output(3, level+msg)
	} else {
		log.SetFlags(log.LstdFlags + log.Lshortfile)
		log.Output(3, level+msg)
	}
}
