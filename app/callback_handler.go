package app

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/line/line-bot-sdk-go/linebot"
	"github.com/sugyan/idol-face-linebot/recognizer"
)

func (app *BotApp) callbackHandler(w http.ResponseWriter, r *http.Request) {
	events, err := app.linebot.ParseRequest(r)
	if err != nil {
		log.Printf("parse request error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		go func(event *linebot.Event) {
			switch event.Type {
			case linebot.EventTypeFollow:
				token, err := app.retrieveUserToken(event.Source.UserID)
				if err != nil {
					log.Print(err)
				}
				log.Printf("token: %v", token)
			case linebot.EventTypeMessage:
				if err := app.handleMessage(event); err != nil {
					log.Print(err)
				}
			case linebot.EventTypePostback:
				if err := app.handlePostback(event); err != nil {
					log.Print(err)
				}
			default:
				log.Printf("not message/postback event: %v (source: %v)", event, *event.Source)
			}
		}(event)
	}
}

func (app *BotApp) handleMessage(event *linebot.Event) error {
	switch message := event.Message.(type) {
	case *linebot.ImageMessage:
		log.Printf("image message from %v: %s", event.Source, message.ID)
		if err := app.sendRecognized(message.ID, event.ReplyToken); err != nil {
			return fmt.Errorf("recognize image error: %v", err)
		}
	}
	return nil
}

func (app *BotApp) handlePostback(event *linebot.Event) error {
	if event.Source.Type != linebot.EventSourceTypeUser {
		return fmt.Errorf("not from user: %v", event)
	}

	userID := event.Source.UserID
	log.Printf("got postback: %s", event.Postback.Data)
	token, err := app.retrieveUserToken(userID)
	if err != nil {
		return err
	}
	client, err := recognizer.NewClient(userID+"@line.me", token)
	if err != nil {
		return err
	}
	// unmarshal data
	data := &postbackData{}
	if err := json.Unmarshal([]byte(event.Postback.Data), data); err != nil {
		return err
	}
	// accept or reject
	var text string
	switch data.Action {
	case postbackActionAccept:
		if err := client.AcceptInference(data.InferenceID); err != nil {
			log.Printf("accept error: %v", err)
			text = "処理できませんでした\xf0\x9f\x98\x9e"
		} else {
			text = fmt.Sprintf("ID:%d を更新しました \xf0\x9f\x99\x86", data.FaceID)
		}
	case postbackActionReject:
		if err := client.RejectInference(data.InferenceID); err != nil {
			log.Printf("reject error: %v", err)
			text = "処理できませんでした\xf0\x9f\x98\x9e"
		} else {
			text = fmt.Sprintf("ID:%d を更新しました \xf0\x9f\x99\x85", data.FaceID)
		}
	}
	if _, err := app.linebot.ReplyMessage(
		event.ReplyToken,
		linebot.NewTextMessage(text),
	).Do(); err != nil {
		return fmt.Errorf("send message error: %v", err)
	}
	return nil
}
