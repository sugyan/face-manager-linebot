package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/line/line-bot-sdk-go/linebot"
	"github.com/sugyan/face-manager-linebot/recognizer"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func main() {
	bot, err := linebot.New(
		os.Getenv("CHANNEL_SECRET"),
		os.Getenv("CHANNEL_TOKEN"),
	)
	if err != nil {
		log.Fatal(err)
	}
	app := &app{
		bot: bot,
		config: recognizerConfig{
			EndpointBase: os.Getenv("RECOGNIZER_API_ENDPOINT"),
			AdminEmail:   os.Getenv("RECOGNIZER_ADMIN_EMAIL"),
			AdminToken:   os.Getenv("RECOGNIZER_ADMIN_TOKEN"),
		},
	}
	http.HandleFunc(os.Getenv("CALLBACK_PATH"), app.handler)
	http.HandleFunc("/thumbnail", thumbnailHandler)
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), nil); err != nil {
		log.Fatal(err)
	}
}

type app struct {
	bot    *linebot.Client
	config recognizerConfig
}

type recognizerConfig struct {
	EndpointBase string
	AdminEmail   string
	AdminToken   string
}

func (a *app) handler(w http.ResponseWriter, r *http.Request) {
	events, err := a.bot.ParseRequest(r)
	if err != nil {
		log.Printf("parse request error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		if event.Source.Type != linebot.EventSourceTypeUser {
			log.Printf("not from user: %v", event)
			continue
		}
		userID := event.Source.UserID
		switch event.Type {
		case linebot.EventTypeFollow:
			token, err := a.getRecognizerToken(userID)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("token: %v", token)
		case linebot.EventTypeMessage:
			if message, ok := event.Message.(*linebot.TextMessage); ok {
				log.Printf("text message from %s: %v", event.Source.UserID, message.Text)
				query := message.Text
				if message.Text == "all" {
					query = ""
				}
				if err := a.sendCarousel(event.Source.UserID, event.ReplyToken, query); err != nil {
					log.Printf("send error: %v", err)
				}
			}
		case linebot.EventTypePostback:
			log.Printf("got postback: %s", event.Postback.Data)
			token, err := a.getRecognizerToken(userID)
			if err != nil {
				log.Fatal(err)
			}
			client, err := recognizer.NewClient(a.config.EndpointBase, userID+"@line.me", token)
			if err != nil {
				log.Fatal(err)
			}
			// <face-id>,<inference-id>
			ids := strings.Split(event.Postback.Data, ",")
			resultURL, err := client.AcceptInference(ids[1])
			if err != nil {
				log.Printf("accept error: %v", err)
				continue
			}
			if _, err := a.bot.ReplyMessage(
				event.ReplyToken,
				linebot.NewTemplateMessage(
					"template message",
					linebot.NewConfirmTemplate(
						fmt.Sprintf("id:%s を更新しました！", ids[0]),
						linebot.NewMessageTemplateAction("やっぱちがう", "やっぱちがう"),
						linebot.NewURITemplateAction("確認する", resultURL),
					),
				),
			).Do(); err != nil {
				log.Printf("send message error: %v", err)
				continue
			}
		default:
			log.Printf("not message/postback event: %v", event)
			continue
		}
	}
}

func (a *app) sendCarousel(userID, replyToken, query string) error {
	token, err := a.getRecognizerToken(userID)
	if err != nil {
		return err
	}
	client, err := recognizer.NewClient(a.config.EndpointBase, userID+"@line.me", token)
	if err != nil {
		return err
	}
	labels, err := client.Labels(query)
	if err != nil {
		return err
	}
	labelIDs := []int{}
	for _, label := range labels {
		labelIDs = append(labelIDs, label.ID)
	}
	inferences, err := client.Inferences(labelIDs)
	if err != nil {
		return err
	}
	if len(inferences) < 1 {
		return errors.New("empty inferences")
	}
	ids := rand.Perm(len(inferences))
	num := 5
	if len(ids) < num {
		num = len(ids)
	}
	columns := make([]*linebot.CarouselColumn, 0, 5)
	for i := 0; i < num; i++ {
		inference := inferences[ids[i]]
		title := fmt.Sprintf("%d:[%.5f] %s", inference.Face.ID, inference.Score, inference.Label.Name)
		if inference.Label.Description != "" {
			title += " (" + strings.Replace(inference.Label.Description, "\r\n", ", ", -1) + ")"
		}
		if len([]rune(title)) > 40 {
			title = string([]rune(title)[0:39]) + "…"
		}
		text := strings.Replace(inference.Face.Photo.Caption, "\n", " ", -1)
		if len([]rune(text)) > 60 {
			text = string([]rune(text)[0:59]) + "…"
		}
		thumbnailImageURL, err := url.Parse(os.Getenv("APP_URL") + "/thumbnail")
		if err != nil {
			return err
		}
		values := url.Values{}
		values.Set("image_url", inference.Face.ImageURL)
		thumbnailImageURL.RawQuery = values.Encode()
		columns = append(
			columns,
			linebot.NewCarouselColumn(
				thumbnailImageURL.String(),
				title,
				text,
				linebot.NewURITemplateAction(
					"\xf0\x9f\x94\x8d くわしく",
					inference.Face.Photo.SourceURL,
				),
				linebot.NewPostbackTemplateAction(
					"\xf0\x9f\x99\x86 あってる",
					strings.Join(
						[]string{
							strconv.FormatUint(uint64(inference.Face.ID), 10),
							strconv.FormatUint(uint64(inference.ID), 10),
						},
						",",
					),
					"",
				),
				linebot.NewMessageTemplateAction(
					"\xf0\x9f\x99\x85 ちがうよ", "ちがうよ",
				),
			),
		)
	}
	if _, err = a.bot.ReplyMessage(
		replyToken,
		linebot.NewTemplateMessage("template message", linebot.NewCarouselTemplate(columns...)),
	).Do(); err != nil {
		return err
	}
	return nil
}

func (a *app) getRecognizerToken(userID string) (string, error) {
	// get profile
	profile, err := a.bot.GetProfile(userID).Do()
	if err != nil {
		return "", err
	}
	// register user and get authentication token as admin
	client, err := recognizer.NewClient(a.config.EndpointBase, a.config.AdminEmail, a.config.AdminToken)
	if err != nil {
		return "", err
	}
	token, err := client.RegisterUser(userID, profile.DisplayName)
	if err != nil {
		return "", err
	}
	return token, nil
}
