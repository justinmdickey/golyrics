package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var colorFlag string

func init() {
	flag.StringVar(&colorFlag, "color", "2", "Set the desired color (name or hex)")
	flag.StringVar(&colorFlag, "c", "2", "Set the desired color (shorthand)")
}

type SongData struct {
	Status string
	Title  string
	Artist string
	Lyrics string
}

type model struct {
	songData   SongData
	color      string
	width      int
	height     int
	lastError  error
	lastSong   string
	fetchingLyrics bool
}

type tickMsg struct{}
type lyricsMsg string

func getSongInfo() (SongData, error) {
	var data SongData

	cmd := exec.Command("playerctl", "metadata", "--format", "{{title}}|{{artist}}|{{status}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return data, errors.New("can't get metadata")
	}

	output := strings.TrimSpace(out.String())
	if output == "" {
		return data, errors.New("no song playing")
	}

	parts := strings.Split(output, "|")
	if len(parts) != 3 {
		return data, errors.New("unexpected metadata format")
	}

	data.Title = strings.TrimSpace(parts[0])
	data.Artist = strings.TrimSpace(parts[1])
	data.Status = strings.TrimSpace(parts[2])

	return data, nil
}

func fetchLyrics(song string) tea.Msg {
	searchURL := fmt.Sprintf("https://genius.com/search?q=%s", strings.ReplaceAll(song, " ", "%20"))

	resp, err := http.Get(searchURL)
	if err != nil {
		return lyricsMsg("Error fetching lyrics")
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return lyricsMsg("Error parsing search results")
	}

	var lyricsURL string
	doc.Find("a[class^='SearchResultSong']").Each(func(i int, s *goquery.Selection) {
		if i == 0 {
			lyricsURL, _ = s.Attr("href")
		}
	})

	if lyricsURL == "" {
		return lyricsMsg("No lyrics found")
	}

	resp, err = http.Get(lyricsURL)
	if err != nil {
		return lyricsMsg("Error fetching lyrics page")
	}
	defer resp.Body.Close()

	doc, err = goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return lyricsMsg("Error parsing lyrics page")
	}

	var lyrics strings.Builder
	doc.Find("div[class^='Lyrics__Container']").Each(func(i int, s *goquery.Selection) {
		// Replace <br> with newlines
		s.Find("br").Each(func(i int, s *goquery.Selection) {
			s.ReplaceWithHtml("\n")
		})
		lyrics.WriteString(s.Text() + "\n")
	})

	if lyrics.Len() == 0 {
		return lyricsMsg("No lyrics found")
	}

	// Clean up the lyrics
	cleanLyrics := strings.ReplaceAll(lyrics.String(), "[", "\n[")
	cleanLyrics = strings.ReplaceAll(cleanLyrics, "]", "]\n")
	cleanLyrics = strings.ReplaceAll(cleanLyrics, "\n\n\n", "\n\n")

	return lyricsMsg(cleanLyrics)
}

func (m model) Init() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "p":
			controlPlayer("play-pause")
		case "n":
			controlPlayer("next")
		case "b":
			controlPlayer("previous")
		case "r":
			if m.songData.Title != "" && m.songData.Artist != "" {
				m.fetchingLyrics = true
				return m, tea.Batch(
					func() tea.Msg {
						return fetchLyrics(m.songData.Artist + " " + m.songData.Title)
					},
				)
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case lyricsMsg:
		m.fetchingLyrics = false
		m.songData.Lyrics = string(msg)
	case tickMsg:
		data, err := getSongInfo()
		if err != nil {
			m.lastError = err
		} else {
			currentSong := data.Artist + " " + data.Title
			if currentSong != m.lastSong && !m.fetchingLyrics {
				m.lastSong = currentSong
				m.fetchingLyrics = true
				m.songData = data
				return m, tea.Batch(
					tea.Tick(time.Second, func(time.Time) tea.Msg {
						return tickMsg{}
					}),
					func() tea.Msg {
						return fetchLyrics(currentSong)
					},
				)
			}
			m.songData.Status = data.Status
			m.lastError = nil
		}
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg {
			return tickMsg{}
		})
	}
	return m, nil
}

func (m model) View() string {
	color := lipgloss.Color(m.color)
	highlight := lipgloss.NewStyle().Foreground(color)

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(1, 2)

	titleStyle := lipgloss.NewStyle().
		Foreground(color).
		Bold(true)

	labelStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

	var content strings.Builder

	if m.lastError != nil {
		content.WriteString(errorStyle.Render("Error: " + m.lastError.Error()))
	} else {
		addLine := func(label, value string) {
			if value != "" {
				content.WriteString(
					fmt.Sprintf("%s %s\n",
						labelStyle.Render(label),
						value,
					),
				)
			}
		}

		addLine("Title: ", m.songData.Title)
		addLine("Artist:", m.songData.Artist)
		addLine("Status:", m.songData.Status)

		if m.fetchingLyrics {
			content.WriteString("\nFetching lyrics...")
		} else if m.songData.Lyrics != "" {
			content.WriteString("\nLyrics:\n" + m.songData.Lyrics)
		}
	}

	contentStr := borderStyle.
		Width(60).
		Render(titleStyle.Render("                Now Playing") + "\n\n" + content.String())

	helpText := lipgloss.JoinHorizontal(
		lipgloss.Center,
		"Play/Pause: "+highlight.Render("p"),
		"  Next: "+highlight.Render("n"),
		"  Previous: "+highlight.Render("b"),
		"  Refresh Lyrics: "+highlight.Render("r"),
		"  Quit: "+highlight.Render("q"),
	)

	fullUI := lipgloss.JoinVertical(lipgloss.Center, contentStr, "\n"+helpText)

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		fullUI,
	)
}

func controlPlayer(command string) error {
	return exec.Command("playerctl", command).Run()
}

func main() {
	flag.Parse()

	initialModel := model{
		color: colorFlag,
	}

	if _, err := tea.NewProgram(initialModel, tea.WithAltScreen()).Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
