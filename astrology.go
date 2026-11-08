package main

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type personInput struct {
	Name      string `json:"name"`
	BirthDate string `json:"birthDate"`
}
type matchRequest struct {
	First  personInput `json:"first"`
	Second personInput `json:"second"`
}
type personOutput struct {
	Name    string `json:"name"`
	Sign    string `json:"sign"`
	Glyph   string `json:"glyph"`
	Element string `json:"element"`
	Title   string `json:"title"`
}
type insight struct {
	Icon  string `json:"icon"`
	Title string `json:"title"`
	Body  string `json:"body"`
}
type matchOutput struct {
	First       personOutput `json:"first"`
	Second      personOutput `json:"second"`
	Score       int          `json:"score"`
	Headline    string       `json:"headline"`
	Subheadline string       `json:"subheadline"`
	Description string       `json:"description"`
	Highlights  []string     `json:"highlights"`
	Insights    []insight    `json:"insights"`
	Metrics     []struct {
		Label string `json:"label"`
		Value int    `json:"value"`
	} `json:"metrics"`
	Engine     string `json:"engine"`
	Disclaimer string `json:"disclaimer"`
}

type signInfo struct{ Key, Name, Glyph, Element, Title, Strength, Needs string }

var signs = []signInfo{
	{"aries", "Aries", "♈", "Fire", "The Trailblazer", "bold initiative", "room to move"},
	{"taurus", "Taurus", "♉", "Earth", "The Steady Heart", "grounded devotion", "consistency"},
	{"gemini", "Gemini", "♊", "Air", "The Curious Spark", "playful conversation", "space to explore"},
	{"cancer", "Cancer", "♋", "Water", "The Gentle Soul", "emotional attentiveness", "a sense of safety"},
	{"leo", "Leo", "♌", "Fire", "The Golden Flame", "warm generosity", "to be appreciated"},
	{"virgo", "Virgo", "♍", "Earth", "The Thoughtful One", "quiet care", "clear communication"},
	{"libra", "Libra", "♎", "Air", "The Harmonizer", "natural diplomacy", "mutual consideration"},
	{"scorpio", "Scorpio", "♏", "Water", "The Deep Current", "focused loyalty", "emotional honesty"},
	{"sagittarius", "Sagittarius", "♐", "Fire", "The Free Spirit", "open-hearted optimism", "a little adventure"},
	{"capricorn", "Capricorn", "♑", "Earth", "The Builder", "patient commitment", "dependability"},
	{"aquarius", "Aquarius", "♒", "Air", "The Original", "fresh perspectives", "independence"},
	{"pisces", "Pisces", "♓", "Water", "The Dreamer", "tender imagination", "gentle understanding"},
}

func isSign(s string) bool {
	for _, v := range signs {
		if v.Key == s {
			return true
		}
	}
	return false
}
func signByKey(s string) signInfo {
	for _, v := range signs {
		if v.Key == s {
			return v
		}
	}
	return signs[0]
}
func sunSign(date time.Time) string {
	m, d := int(date.Month()), date.Day()
	switch {
	case (m == 3 && d >= 21) || (m == 4 && d <= 19):
		return "aries"
	case (m == 4 && d >= 20) || (m == 5 && d <= 20):
		return "taurus"
	case (m == 5 && d >= 21) || (m == 6 && d <= 20):
		return "gemini"
	case (m == 6 && d >= 21) || (m == 7 && d <= 22):
		return "cancer"
	case (m == 7 && d >= 23) || (m == 8 && d <= 22):
		return "leo"
	case (m == 8 && d >= 23) || (m == 9 && d <= 22):
		return "virgo"
	case (m == 9 && d >= 23) || (m == 10 && d <= 22):
		return "libra"
	case (m == 10 && d >= 23) || (m == 11 && d <= 21):
		return "scorpio"
	case (m == 11 && d >= 22) || (m == 12 && d <= 21):
		return "sagittarius"
	case (m == 12 && d >= 22) || (m == 1 && d <= 19):
		return "capricorn"
	case (m == 1 && d >= 20) || (m == 2 && d <= 18):
		return "aquarius"
	default:
		return "pisces"
	}
}
func validatePerson(p personInput) (personOutput, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 40 {
		return personOutput{}, errors.New("Names must be 1–40 characters")
	}
	if len(p.BirthDate) != 10 {
		return personOutput{}, errors.New("Choose a valid birth date")
	}
	d, err := time.Parse("2006-01-02", p.BirthDate)
	if err != nil || d.Year() < 1900 || d.After(time.Now().UTC().Truncate(24*time.Hour)) {
		return personOutput{}, errors.New("Birth date must be between 1900 and today")
	}
	info := signByKey(sunSign(d))
	return personOutput{p.Name, info.Key, info.Glyph, info.Element, info.Title}, nil
}
func buildMatch(req matchRequest) (matchOutput, error) {
	a, err := validatePerson(req.First)
	if err != nil {
		return matchOutput{}, fmt.Errorf("First person: %w", err)
	}
	b, err := validatePerson(req.Second)
	if err != nil {
		return matchOutput{}, fmt.Errorf("Second person: %w", err)
	}
	return makeMatch(a, b), nil
}
func buildFromSigns(s1, s2, n1, n2 string) matchOutput {
	a, b := signByKey(s1), signByKey(s2)
	return makeMatch(personOutput{n1, a.Key, a.Glyph, a.Element, a.Title}, personOutput{n2, b.Key, b.Glyph, b.Element, b.Title})
}
func makeMatch(a, b personOutput) matchOutput {
	first, second := signByKey(a.Sign), signByKey(b.Sign)
	pair1, pair2 := a.Sign, b.Sign
	if pair1 > pair2 {
		pair1, pair2 = pair2, pair1
	}
	seed := sha256.Sum256([]byte("astromatcher-v1:" + pair1 + ":" + pair2))
	n := binary.BigEndian.Uint32(seed[:4])
	base := 64
	if first.Element == second.Element {
		base = 78
	}
	complement := map[string]string{"Fire": "Air", "Air": "Fire", "Earth": "Water", "Water": "Earth"}
	if complement[first.Element] == second.Element {
		base = 75
	}
	score := base + int(n%15) - 5
	if score > 95 {
		score = 95
	}
	if score < 52 {
		score = 52
	}
	label := "A surprising connection"
	if score >= 83 {
		label = "Magnetic energy"
	} else if score >= 72 {
		label = "A natural rhythm"
	} else if score >= 62 {
		label = "Beautiful contrasts"
	}
	theme := elementStory(first.Element, second.Element)
	metrics := []struct {
		Label string `json:"label"`
		Value int    `json:"value"`
	}{
		{"Chemistry", clamp(score + int(seed[5]%13) - 6)},
		{"Communication", clamp(score + int(seed[6]%15) - 8)},
		{"Emotional flow", clamp(score + int(seed[7]%17) - 9)},
	}
	return matchOutput{
		First: a, Second: b, Score: score, Headline: label,
		Subheadline: first.Name + " & " + second.Name,
		Description: fmt.Sprintf("In this astrology-inspired story, %s brings %s while %s brings %s. %s", first.Name, first.Strength, second.Name, second.Strength, theme),
		Highlights:  []string{first.Name + ": " + first.Title, second.Name + ": " + second.Title, "Your story is yours to write"},
		Insights: []insight{
			{Icon: "spark", Title: "What draws you together", Body: fmt.Sprintf("The traditional %s and %s archetypes create a playful contrast. %s could be drawn to %s, while %s brings %s.", first.Name, second.Name, first.Name, second.Strength, second.Name, first.Strength)},
			{Icon: "moon", Title: "Your emotional rhythm", Body: fmt.Sprintf("Astrology associates %s with %s and %s with %s. A little curiosity about how each of you feels can matter more than a perfect match on paper.", first.Name, first.Needs, second.Name, second.Needs)},
			{Icon: "orbit", Title: "Where you can grow", Body: fmt.Sprintf("For this pairing, the interesting question is how to balance %s with %s. Treat the reading as a conversation starter, not a verdict on your relationship.", first.Needs, second.Needs)},
		},
		Metrics: metrics, Engine: "sun-sign-demo-v1", Disclaimer: "For entertainment and reflection only. Based on approximate Sun-sign dates, not a full birth chart. The score is illustrative and has no scientific predictive validity.",
	}
}
func clamp(v int) int {
	if v < 48 {
		return 48
	}
	if v > 96 {
		return 96
	}
	return v
}
func elementStory(a, b string) string {
	if a == b {
		return "Sharing an element can feel familiar, while leaving room to discover your differences."
	}
	if (a == "Fire" && b == "Air") || (a == "Air" && b == "Fire") {
		return "In traditional astrology, air feeds fire: ideas and energy can travel together."
	}
	if (a == "Earth" && b == "Water") || (a == "Water" && b == "Earth") {
		return "In traditional astrology, earth and water can make a gentle, grounding combination."
	}
	return "Contrasting elements can inspire curiosity and invite you to meet each other halfway."
}
