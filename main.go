package main

import (
	"github.com/BurntSushi/toml"
	"github.com/go-acme/lego/log"
	"github.com/godbus/dbus"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

var exeDir = filepath.Dir(os.Args[0])

type Rule struct {
	MustContain string `toml:"must_contain"`
	RunCommand  string `toml:"run_command"`
}

type Config struct {
	Rules []Rule `toml:"rule"`
}

var config Config

func main() {
	log.Println("Reading config")
	_, err := toml.DecodeFile(filepath.Join(exeDir, "config.toml"), &config)
	if err != nil {
		log.Fatal("Error reading config:", err)
	}

	log.Println("Connecting to system D-Bus")
	conn, err := dbus.SystemBus()
	if err != nil {
		log.Fatal("Error connecting:", err)
	}

	log.Println("Registering for incoming message notifications")
	err = conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.ModemManager1.Modem.Messaging"), dbus.WithMatchMember("Added"))
	if err != nil {
		log.Fatal("Error registering match rule:", err)
	}

	log.Println("Initial processing of all messages")
	processAllMessages(conn)

	log.Println("Waiting for messages")
	signals := make(chan *dbus.Signal)
	conn.Signal(signals)

	for _ = range signals {
		processAllMessages(conn)
	}
}

func processAllMessages(conn *dbus.Conn) {

	log.Println("Enumerating messages")

	var resp map[dbus.ObjectPath]map[string]map[string]dbus.Variant

	err := conn.Object("org.freedesktop.ModemManager1", "/org/freedesktop/ModemManager1").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&resp)
	if err != nil {
		log.Fatal("Error enumerating messages:", err)
	}

	type Message struct {
		MessagePath dbus.ObjectPath
		ModemPath   dbus.ObjectPath
	}

	allMessages := make([]Message, 0)
	for modemPath, modemObj := range resp {
		const messagingInterface = "org.freedesktop.ModemManager1.Modem.Messaging"
		const messagesProp = "Messages"
		for _, p := range modemObj[messagingInterface][messagesProp].Value().([]dbus.ObjectPath) {
			allMessages = append(allMessages, Message{MessagePath: p, ModemPath: modemPath})
		}
	}

	for _, m := range allMessages {
		processMessage(conn, m.MessagePath, m.ModemPath)
	}
}

func processMessage(conn *dbus.Conn, msg dbus.ObjectPath, modem dbus.ObjectPath) {

	log.Println("Processing message " + path.Base(string(msg)))

	vText, err := conn.Object("org.freedesktop.ModemManager1", msg).GetProperty("org.freedesktop.ModemManager1.Sms.Text")
	if err != nil {
		log.Fatal("Error reading message text:", err)
	}
	vNumber, err := conn.Object("org.freedesktop.ModemManager1", msg).GetProperty("org.freedesktop.ModemManager1.Sms.Number")
	if err != nil {
		log.Fatal("Error reading message number:", err)
	}

	processMessageContents(vNumber.String(), vText.String())

	log.Println("Deleting message " + path.Base(string(msg)))
	conn.Object("org.freedesktop.ModemManager1", modem).Call("org.freedesktop.ModemManager1.Modem.Messaging.Delete", 0, msg)

}

func processMessageContents(sender, contents string) {
	for _, rule := range config.Rules {
		if strings.Contains(strings.ToLower(contents), strings.ToLower(rule.MustContain)) {
			log.Println("Rule " + rule.MustContain + " matches, running " + rule.RunCommand)
			go func(rule Rule) {
				output, _ := exec.Command("/bin/sh", "-c", rule.RunCommand).CombinedOutput()
				log.Println("Command finished, output: \n", string(output))
			}(rule)
		}
	}
}
