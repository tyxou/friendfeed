// copyright 2015 The Lastff Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// default crawl client
// go run main.go -c=/srv/httpcache
//
// enque job
// go run main.go -u=name -p=pwd
// go run main.go -u=name -p=pwd -t=friendfeed-feedback
//
// debug archived
// go run main.go -u=yinhm -d=true
package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"time"

	"github.com/ChimeraCoder/anaconda"
	pb "github.com/yinhm/friendfeed/proto"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var config struct {
	address  string
	username string
	file     string
	command  string
	debug    bool
}

type TwitterConfig struct {
	ApiKey    string `json:"twitter_api_key"`
	ApiSecret string `json:"twitter_api_secret"`
}

func init() {
	flag.StringVar(&config.address, "addr", "localhost:8901", "RPC Server Url")
	flag.StringVar(&config.file, "c", "/srv/ff/config.json", "config file")
	flag.StringVar(&config.command, "cmd", "", "cmd execution")
	flag.StringVar(&config.username, "u", "", "debug user feed")
	flag.BoolVar(&config.debug, "d", false, "Enable debug info.")
}

func NewConfigFromJSON(filename string) (*TwitterConfig, error) {
	rawdata, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatal(err)
	}

	config := new(TwitterConfig)
	if err := json.Unmarshal(rawdata, &config); err != nil {
		return nil, err
	}
	return config, nil
}

func randhash() string {
	randbytes := make([]byte, 4)
	rand.Read(randbytes)

	h := sha1.New()
	h.Write(randbytes)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

type FeedAgent struct {
	client pb.ApiClient
	worker *pb.Worker
}

func NewFeedAgent(conn *grpc.ClientConn) *FeedAgent {
	c := pb.NewApiClient(conn)
	worker := &pb.Worker{
		Id: randhash(),
	}
	return &FeedAgent{
		client: c,
		worker: worker,
	}
}

func (fa *FeedAgent) Start() {
	if config.command != "" {
		cmd := &pb.CommandRequest{config.command}
		fa.client.Command(context.Background(), cmd)
		return
	}

	if config.debug && config.username != "" {
		if err := fa.Debug(config.username); err != nil {
			log.Fatalf("Debug failed: %s", err)
		}
		return
	}

	log.Print("start processing...")
	// run feed mirror job forever
	for {
		job, err := fa.newJob()
		if err != nil {
			log.Printf("Get job failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if err := fa.process(job); err != nil {
			log.Printf("Archive failed: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}
	}
}

func (fa *FeedAgent) Debug(name string) error {
	req := &pb.FeedRequest{
		Id:       name,
		Start:    0,
		PageSize: 50,
	}
	feed, err := fa.client.FetchFeed(context.Background(), req)
	if err != nil {
		return err
	}
	log.Printf("feed: %v", feed.Id)
	log.Printf("feed.Entries: %d", len(feed.Entries))

	for _, e := range feed.Entries {
		log.Println(e.Id, e.Date, e.RawBody)
	}
	return nil
}

func (fa *FeedAgent) newJob() (*pb.FeedJob, error) {
	feedjob, err := fa.client.GetFeedJob(context.Background(), fa.worker)
	if err != nil {
		return nil, err
	}
	return feedjob, nil
}

func (fa *FeedAgent) process(job *pb.FeedJob) error {
	log.Printf("Start fetching entries for: %s", job.Id)
	total, err := fa.fetchService(job)
	if err != nil {
		return err
	}

	job, err = fa.client.FinishJob(context.Background(), job)
	if err != nil {
		return err
	}

	log.Printf("Job done for %s, %d entries", job.Id, total)
	return nil
}

func (fa *FeedAgent) fetchService(job *pb.FeedJob) (int, error) {
	stream, err := fa.client.ArchiveFeed(context.Background())
	defer stream.CloseAndRecv()
	if err != nil {
		return 0, err
	}

	authinfo := job.Service.Oauth
	if authinfo == nil {
		return 0, fmt.Errorf("skip job: no authinfo")
	}
	api := anaconda.NewTwitterApi(authinfo.AccessToken, authinfo.AccessTokenSecret)

	v := url.Values{}
	v.Set("screen_name", authinfo.Name)
	tweets, _ := api.GetUserTimeline(v)

	n := 0
	for _, tweet := range tweets {
		fmt.Printf("%s\n", tweet.Text)
		n++
		break
	}
	return n, nil
}

func main() {
	flag.Parse()

	tc, err := NewConfigFromJSON(config.file)
	if err != nil {
		log.Fatal(err)
	}
	anaconda.SetConsumerKey(tc.ApiKey)
	anaconda.SetConsumerSecret(tc.ApiSecret)

	conn, err := grpc.Dial(config.address)
	if err != nil {
		log.Fatalf("Connection error: %v", err)
	}
	defer conn.Close()

	agent := NewFeedAgent(conn)
	agent.Start()
}
