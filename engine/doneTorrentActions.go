package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/asdine/storm"
	Settings "github.com/deranjer/goTorrent/settings"
	Storage "github.com/deranjer/goTorrent/storage"
	pushbullet "github.com/mitsuse/pushbullet-go"
	"github.com/mitsuse/pushbullet-go/requests"
	folderCopy "github.com/otiai10/copy"
	"github.com/sirupsen/logrus"
)

//MoveAndLeaveSymlink takes the file from the default download dir and moves it to the user specified directory and then leaves a symlink behind.
func MoveAndLeaveSymlink(config Settings.FullClientSettings, tHash string, db *storm.DB, moveDone bool, oldPath string) error { //moveDone and oldPath are for moving a completed torrent
	tStorage := Storage.FetchTorrentFromStorage(db, tHash)
	Logger.WithFields(logrus.Fields{"Torrent Name": tStorage.TorrentName}).Info("Move and Create symlink started for torrent")
	var oldFilePath string
	if moveDone { //only occurs on manual move
		oldFilePathTemp := filepath.Join(oldPath, tStorage.TorrentName)
		var err error
		oldFilePath, err = filepath.Abs(oldFilePathTemp)
		if err != nil {
			Logger.WithFields(logrus.Fields{"Torrent Name": tStorage.TorrentName, "Filepath": oldFilePath}).Error("Cannot create absolute file path!")
			moveDone = false
			return err
		}
	} else {
		oldFilePathTemp := filepath.Join(config.TorrentConfig.DataDir, tStorage.TorrentName)
		var err error
		oldFilePath, err = filepath.Abs(oldFilePathTemp)
		if err != nil {
			Logger.WithFields(logrus.Fields{"Torrent Name": tStorage.TorrentName, "Filepath": oldFilePath}).Error("Cannot create absolute file path!")
			moveDone = false
			return err
		}
	}
	newFilePathTemp := filepath.Join(tStorage.StoragePath, tStorage.TorrentName)
	newFilePath, err := filepath.Abs(newFilePathTemp)
	if err != nil {
		Logger.WithFields(logrus.Fields{"Torrent Name": tStorage.TorrentName, "Filepath": newFilePath}).Error("Cannot create absolute file path for new file path!")
		moveDone = false
		return err
	}
	_, err = os.Stat(tStorage.StoragePath)
	if os.IsNotExist(err) {
		err := os.MkdirAll(tStorage.StoragePath, 0777)
		if err != nil {
			Logger.WithFields(logrus.Fields{"New File Path": newFilePath, "error": err}).Error("Cannot create new directory")
			moveDone = false
			return err
		}
	}
	oldFileInfo, err := os.Stat(oldFilePath)
	if err != nil {
		Logger.WithFields(logrus.Fields{"Old File info": oldFileInfo, "Old File Path": oldFilePath, "error": err}).Error("Cannot find the old file to copy/symlink!")
		moveDone = false
		return err
	}

	if oldFilePath != newFilePath {
		newFilePathDir := filepath.Dir(newFilePath)
		os.Mkdir(newFilePathDir, 0777)
		err := folderCopy.Copy(oldFilePath, newFilePath) //copy the folder to the new location
		if err != nil {
			Logger.WithFields(logrus.Fields{"Old File Path": oldFilePath, "New File Path": newFilePath, "error": err}).Error("Error Copying Folder!")
			return err
		}
		err = filepath.Walk(newFilePath, func(path string, info os.FileInfo, err error) error { //Walking the file path to change the permissions
			if err != nil {
				Logger.WithFields(logrus.Fields{"file": path, "error": err}).Error("Potentially non-critical error, continuing..")
			}
			os.Chmod(path, 0777)
			return nil
		})
		/* if runtime.GOOS != "windows" { //TODO the windows symlink is broken on windows 10 creator edition, so on the other platforms create symlink (windows will copy) until Go1.11
			os.RemoveAll(oldFilePath)
			err = os.Symlink(newFilePath, oldFilePath)
			if err != nil {
				Logger.WithFields(logrus.Fields{"Old File Path": oldFilePath, "New File Path": newFilePath, "error": err}).Error("Error creating symlink")
				moveDone = false
				return err
			}
		} */
		if moveDone == false {
			tStorage.TorrentMoved = true     //TODO error handling instead of just saying torrent was moved when it was not
			notifyUser(tStorage, config, db) //Only notify if we haven't moved yet, don't want to push notify user every time user uses change storage button
		}
		Logger.WithFields(logrus.Fields{"Old File Path": oldFilePath, "New File Path": newFilePath}).Info("Moving completed torrent")
		tStorage.StoragePath = filepath.Dir(newFilePath)
		Storage.UpdateStorageTick(db, tStorage)
	}
	return nil
}

func notifyUser(tStorage Storage.TorrentLocal, config Settings.FullClientSettings, db *storm.DB) {
	Logger.WithFields(logrus.Fields{"New File Path": tStorage.StoragePath, "Torrent Name": tStorage.TorrentName}).Info("Attempting to notify user..")
	tStorage.TorrentMoved = true
	//Storage.AddTorrentLocalStorage(db, tStorage) //Updating the fact that we moved the torrent
	Storage.UpdateStorageTick(db, tStorage)
	if config.PushBulletToken != "" {
		pb := pushbullet.New(config.PushBulletToken)
		n := requests.NewNote()
		n.Title = tStorage.TorrentName
		n.Body = "Completed and moved to " + tStorage.StoragePath
		if _, err := pb.PostPushesNote(n); err != nil {
			Logger.WithFields(logrus.Fields{"Torrent": tStorage.TorrentName, "New File Path": tStorage.StoragePath, "error": err}).Error("Error pushing PushBullet Note")
			return
		}
		Logger.WithFields(logrus.Fields{"Torrent": tStorage.TorrentName, "New File Path": tStorage.StoragePath}).Info("Pushbullet note sent")
	} else {
		Logger.WithFields(logrus.Fields{"New File Path": tStorage.StoragePath, "Torrent Name": tStorage.TorrentName}).Info("No pushbullet API key set, not notifying")
	}

	if config.NotifyCommand != "" {
		cmd := exec.Command(config.NotifyCommand)
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("DIR=%s", tStorage.StoragePath),
			fmt.Sprintf("PATH=%s", tStorage.TorrentFileName),
			fmt.Sprintf("SIZE=%d", tStorage.TorrentSize),
			fmt.Sprintf("FILECNT=%d", len(tStorage.TorrentFile)))

		stdoutStderr, err := cmd.CombinedOutput()
		if err != nil {
			Logger.WithFields(logrus.Fields{"Torrent Name": tStorage.TorrentName, "New File Path": tStorage.StoragePath}).Error("NotifyCommand called error:", err)
		}
		Logger.WithFields(logrus.Fields{"Torrent Name": tStorage.TorrentName, "New File Path": tStorage.StoragePath}).Info("NotifyCommand called output:", stdoutStderr)
	}
}
