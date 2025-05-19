package main

import (
	"bufio"
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
	STD_INFO_PREFIX  = "[\033[32mINFO\033[0m]"
	STD_ERROR_PREFIX = "[\033[31mERROR\033[0m]"

	INFO_PREFIX  = "[INFO]"
	ERROR_PREFIX = "[ERROR]"
)

// 定义日志消息结构体
type LogMessage struct {
	Prefix  string
	Message string
}

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

	CacheFile string `json:"cache_file"`
}

var (
	info_log  = log.New(os.Stdout, STD_INFO_PREFIX+" ", log.LstdFlags)
	error_log = log.New(os.Stderr, STD_ERROR_PREFIX+" ", log.LstdFlags)

	cacheMutex sync.Mutex

	cacheFileWriter *os.File
	cacheBuffer     []string
	// 定义批量写入的阈值
	batchSize = 1000
)

func main() {
	config, err := readConfig("config.json")
	if err != nil {
		fmt.Println(STD_ERROR_PREFIX, "读取配置文件失败: ", err.Error())
		return
	}
	fmt.Println(STD_INFO_PREFIX, "读取配置文件成功")

	if config.InfoLog != "" {
		file, err := os.OpenFile(config.InfoLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			fmt.Println(STD_ERROR_PREFIX, "打开INFO日志文件失败，使用标准输出: ", err.Error())
		} else {
			defer file.Close()
			info_log.SetOutput(file)
			info_log.SetPrefix(INFO_PREFIX + " ")
			fmt.Println(STD_INFO_PREFIX, "日志文件INFO设置成功")
		}
	}

	if config.ErrorLog != "" {
		file, err := os.OpenFile(config.ErrorLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			fmt.Println(STD_ERROR_PREFIX, "打开ERROR日志文件失败，使用标准输出: ", err.Error())
		} else {
			defer file.Close()
			error_log.SetOutput(file)
			error_log.SetPrefix(ERROR_PREFIX + " ")
			fmt.Println(STD_INFO_PREFIX, "日志文件ERROR设置成功")
		}
	}

	cacheFileWriter, err = os.OpenFile(config.CacheFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println(STD_INFO_PREFIX, "打开缓存文件失败: ", config.CacheFile, " ", err.Error())
	}
	fmt.Println(STD_INFO_PREFIX, "缓存文件打开成功")

	processedFiles := readProcessedFiles(config.CacheFile)

	des := xdes.NewTripleDes(config.Key, config.IV)

	cpu_num := runtime.NumCPU()
	fmt.Println(STD_INFO_PREFIX, "当前CPU总数: ", cpu_num)

	current_cpu_num := config.CPUNum
	if current_cpu_num <= 0 {
		current_cpu_num = int64(cpu_num / 2)
	}
	runtime.GOMAXPROCS(int(current_cpu_num))
	fmt.Println(STD_INFO_PREFIX, "当前解密CPU数量: ", current_cpu_num) // 输出到标准输出

	var wg sync.WaitGroup
	current_coroutine := config.Coroutine
	if current_coroutine <= 0 {
		current_coroutine = int64(current_cpu_num + 1)
	}
	sem := make(chan struct{}, current_coroutine)
	fmt.Println(STD_INFO_PREFIX, "当前解密并发量: ", current_coroutine) // 输出到标准输出

	fmt.Println(STD_INFO_PREFIX, "解密后数据的体积约为源数据的3/4")

	info_log.Println("开始解密")

	start_time := time.Now()

	filepath.Walk(config.InputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".webp" {
			if _, exists := processedFiles[path]; exists {
				info_log.Println("跳过已处理文件: ", path)
				return nil
			}

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

				// 写入相对路径到缓存文件
				recordProcessedFile(config.CacheFile, inputPath)

				info_log.Println("解密文件成功: ", inputPath, " -> ", outputPath)
			}(path)
		}
		return nil
	})

	wg.Wait()

	// 确保所有缓冲区的数据都写入文件
	flushCacheBuffer()

	if cacheFileWriter != nil {
		cacheFileWriter.Close()
	}

	elapsed_time := time.Since(start_time).Seconds()

	info_log.Println("解密完成")

	fmt.Println(STD_INFO_PREFIX, "解密完成，本次解密总耗时:", elapsed_time, "秒")
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

// 读取已处理的文件列表
func readProcessedFiles(cache_file string) map[string]bool {
	processedFiles := make(map[string]bool)
	file, err := os.Open(cache_file)
	if err != nil {
		return processedFiles
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		processedFiles[scanner.Text()] = true
	}

	return processedFiles
}

// 记录已处理的文件
func recordProcessedFile(cacheFile, filePath string) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	cacheBuffer = append(cacheBuffer, filePath)
	if len(cacheBuffer) >= batchSize {
		flushCacheBuffer()
	}
}

// 批量写入缓冲区数据到文件
func flushCacheBuffer() {
	if cacheFileWriter == nil {
		return
	}

	for _, path := range cacheBuffer {
		if _, err := cacheFileWriter.WriteString(path + "\n"); err != nil {
			error_log.Println("写入缓存文件失败: ", err.Error())
		}
	}
	// 清空缓冲区
	cacheBuffer = cacheBuffer[:0]
}
