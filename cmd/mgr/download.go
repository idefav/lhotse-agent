package mgr

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
)

var (
	baseUrl string
)

func Download(appName string, version string) error {
	var url = "http://localhost:18888/api/download?appName=" + appName + "&version=" + version
	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}
	exPath := ex
	var newVersionPath = exPath + "-" + version
	newVersionFile, err := os.Create(newVersionPath)
	if err != nil {
		return err
	}
	err = os.Chmod(newVersionPath, fs.ModePerm.Perm())
	if err != nil {
		return err
	}
	client := http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			r.URL.Opaque = r.URL.Path
			return nil
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New("获取指定版本程序失败")
	}

	_, err = io.Copy(newVersionFile, resp.Body)

	defer newVersionFile.Close()

	if err != nil {
		return err
	}
	var bakFile = exPath + "-bak"
	os.Remove(bakFile)

	err = os.Rename(exPath, bakFile)
	if err != nil {
		return err
	}
	err = os.Rename(newVersionPath, exPath)
	if err != nil {
		return err
	}
	return nil
}
