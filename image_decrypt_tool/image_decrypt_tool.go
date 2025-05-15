package main

import (
	"cartoon/pkg/xdes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	INFO_PREFIX  = "[\033[32mINFO\033[0m]"
	ERROR_PREFIX = "[\033[31mERROR\033[0m]"
)

// 定义日志消息结构体
type LogMessage struct {
	Prefix  string
	Message string
}

// 定义日志通道
var logChannel = make(chan LogMessage, 1000)

// Config 定义配置结构体
type Config struct {
	InputDir  string `json:"input_dir"`
	OutputDir string `json:"output_dir"`

	InfoLog  string `json:"info_log"`
	ErrorLog string `json:"error_log"`

	Key string `json:"key"`
	IV  string `json:"iv"`

	CPUNum int64 `json:"cpu_num"`

	Coroutine int64 `json:"coroutine"`
}

var (
	info_log  = log.New(os.Stdout, INFO_PREFIX, log.LstdFlags)
	error_log = log.New(os.Stderr, ERROR_PREFIX, log.LstdFlags)
)

func main() {
	config, err := readConfig("config.json")
	if err != nil {
		fmt.Println(ERROR_PREFIX, "读取配置文件失败: ", err.Error())
		return
	}
	fmt.Println(INFO_PREFIX, "读取配置文件成功")

	if config.InfoLog != "" {
		file, err := os.OpenFile(config.InfoLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			fmt.Println(ERROR_PREFIX, "打开INFO日志文件失败，使用标准输出: ", err.Error())
		} else {
			defer file.Close()
			info_log.SetOutput(file)
			fmt.Println(INFO_PREFIX, "日志文件INFO设置成功")
		}
	}

	if config.ErrorLog != "" {
		file, err := os.OpenFile(config.ErrorLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			fmt.Println(ERROR_PREFIX, "打开ERROR日志文件失败，使用标准输出: ", err.Error())
		} else {
			defer file.Close()
			error_log.SetOutput(file)
			fmt.Println(INFO_PREFIX, "日志文件ERROR设置成功")
		}
	}

	des := xdes.NewTripleDes(config.Key, config.IV)

	cpu_num := runtime.NumCPU()
	fmt.Println(INFO_PREFIX, "当前CPU总数: ", cpu_num)

	current_cpu_num := config.CPUNum
	if current_cpu_num <= 0 {
		current_cpu_num = int64(cpu_num / 2)
	}
	runtime.GOMAXPROCS(int(current_cpu_num))
	fmt.Println(INFO_PREFIX, "当前解密CPU数量: ", current_cpu_num) // 输出到标准输出

	var wg sync.WaitGroup
	current_coroutine := config.Coroutine
	if current_coroutine <= 0 {
		current_coroutine = int64(current_cpu_num)
	}
	sem := make(chan struct{}, current_coroutine)
	fmt.Println(INFO_PREFIX, "当前解密并发量: ", current_coroutine) // 输出到标准输出

	fmt.Println(INFO_PREFIX, "解密时长参考配置: 16核处理器, 32GB内存")
	fmt.Println(INFO_PREFIX, "解密时长参考（8核8协程）: 6.81MB/s")
	fmt.Println(INFO_PREFIX, "解密时长参考（8核12协程）: 26.41MB/s")
	fmt.Println(INFO_PREFIX, "解密时长参考（8核16协程）: 24.52MB/s")
	fmt.Println(INFO_PREFIX, "解密时长参考（12核12协程）: 27.12MB/s")
	fmt.Println(INFO_PREFIX, "解密时长参考（12核16协程）: 23.64MB/s")
	fmt.Println(INFO_PREFIX, "解密时长参考（12核24协程）: 24.69MB/s")
	fmt.Println(INFO_PREFIX, "解密后数据的体积约为源数据的3/4")

	start_time := time.Now()

	filepath.Walk(config.InputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".webp" {
			wg.Add(1)
			sem <- struct{}{}
			go func(inputPath string) {
				defer wg.Done()
				defer func() { <-sem }()

				relPath, err := filepath.Rel(config.InputDir, inputPath)
				if err != nil {
					error_log.Println("获取相对路径失败: ", inputPath, " ", err.Error())
					return
				}
				outputPath := filepath.Join(config.OutputDir, relPath)

				err = decryptFile(inputPath, outputPath, des)
				if err != nil {
					error_log.Println("解密文件失败: ", inputPath, " ", err.Error())
				}

				info_log.Println("解密文件成功: ", inputPath, " -> ", outputPath)
			}(path)
		}
		return nil
	})

	wg.Wait()

	elapsed_time := time.Since(start_time).Seconds()
	fmt.Println(INFO_PREFIX, "解密完成，本次解密总耗时:", elapsed_time, "秒")
}

// 读取配置文件
func readConfig(filePath string) (*Config, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var config Config
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// 解密文件
func decryptFile(inputPath, outputPath string, des *xdes.TripleDES) error {
	readFile, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer readFile.Close()

	origin, err := io.ReadAll(readFile)
	if err != nil {
		return err
	}

	str, err := des.DecryptCBC(string(origin))
	if err != nil {
		return err
	}

	// 创建输出目录
	err = os.MkdirAll(filepath.Dir(outputPath), 0755)
	if err != nil {
		return err
	}

	writeFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer writeFile.Close()

	_, err = writeFile.WriteString(str)
	return err
}
