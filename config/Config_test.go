package config

import (
	"bufio"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	testEnvFile string = ".testenv"
	testConfig  AppConfig
)

func checkErr(err error) {
	if err != nil {
		panic(fmt.Sprintf("could not execute test preparation. Error: %s", err))
	}
}

func writeTestEnv(fileName string) {
	f, err := os.Create(fileName)
	checkErr(err)
	defer f.Close()
	w := bufio.NewWriter(f)
	_, err = w.WriteString("CALCMS_URL=\"http://localhost/\"\n")
	checkErr(err)
	w.Flush()
}

func deleteEnvFile(fileName string) {
	err := os.Remove(fileName)
	checkErr(err)
}

func TestLoadConfigNoEnvFileReturnsError(t *testing.T) {
	err := loadConfig("file_does_not_exist.txt")
	assert.NotNil(t, err)
	fmt.Printf("error: %v", err)

	assert.EqualValues(t, "open file_does_not_exist.txt: The system cannot find the file specified.", err.Error())
}

func TestLoadConfigWithEnvFileReturnsNoError(t *testing.T) {
	writeTestEnv(testEnvFile)
	defer deleteEnvFile(testEnvFile)
	err := loadConfig(testEnvFile)

	assert.Nil(t, err)
	assert.EqualValues(t, "http://localhost/", os.Getenv("CALCMS_URL"))
}

func TestCheckFilePathEmptyPathKeepsPathEmpty(t *testing.T) {
	testPath := ""
	checkFilePath(&testPath)
	assert.EqualValues(t, "", testPath)
}

func TestCheckFilePathCorrectPathReturnsCorrectPath(t *testing.T) {
	testPath := "C:\\TEMP"
	checkFilePath(&testPath)
	assert.EqualValues(t, "C:\\temp", testPath)
}

func TestCheckFilePathWeirdPathReturnsCorrectPath(t *testing.T) {
	testPath := "C:\\TEMP\\..\\..\\..\\etc"
	checkFilePath(&testPath)
	assert.EqualValues(t, "C:\\etc", testPath)
}
