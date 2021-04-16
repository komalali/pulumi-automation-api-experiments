package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pulumi/pulumi-aws/sdk/v4/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// pulumiProgram is the Pulumi program itself where resources are declared. It deploys a simple static website to S3.
func pulumiProgram(ctx *pulumi.Context) error {
	// similar go git_repo_program, our program defines a s3 website.
	// here we create the bucket
	siteBucket, err := s3.NewBucket(ctx, "s3-website-bucket", &s3.BucketArgs{
		Website: s3.BucketWebsiteArgs{
			IndexDocument: pulumi.String("index.html"),
		},
	})
	if err != nil {
		return err
	}

	// we define and upload our HTML inline.
	indexContent := `<html><head>
		<title>Hello S3</title><meta charset="UTF-8">
	</head>
	<body><p>Hello, world!</p><p>Made with ❤️ with <a href="https://pulumi.com">Pulumi</a></p>
	</body></html>
`
	// upload our index.html
	if _, err := s3.NewBucketObject(ctx, "index", &s3.BucketObjectArgs{
		Bucket:      siteBucket.ID(), // reference to the s3.Bucket object
		Content:     pulumi.String(indexContent),
		Key:         pulumi.String("index.html"),               // set the key of the object
		ContentType: pulumi.String("text/html; charset=utf-8"), // set the MIME type of the file
	}); err != nil {
		return err
	}

	// Set the access policy for the bucket so all objects are readable.
	if _, err := s3.NewBucketPolicy(ctx, "bucketPolicy", &s3.BucketPolicyArgs{
		Bucket: siteBucket.ID(), // refer to the bucket created earlier
		Policy: pulumi.Any(map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{
				{
					"Effect":    "Allow",
					"Principal": "*",
					"Action": []interface{}{
						"s3:GetObject",
					},
					"Resource": []interface{}{
						pulumi.Sprintf("arn:aws:s3:::%s/*", siteBucket.ID()), // policy refers to bucket name explicitly
					},
				},
			},
		}),
	}); err != nil {
		return err
	}

	// export the website URL
	ctx.Export("websiteUrl", siteBucket.WebsiteEndpoint)
	return nil
}

// runPulumiUpdate runs the update or destroy commands based on input.
// It takes as arguments a flag to determine update or destroy, a channel to receive log messages
// and another to receive structured events from the Pulumi Engine.
func runPulumiUpdate(destroy bool, logChannel chan<- logMessage, eventChannel chan<- events.EngineEvent) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		projectName := "inlineS3Project"
		// we use a simple stack name here, but recommend using auto.FullyQualifiedStackName for maximum specificity.
		stackName := "dev"
		// stackName := auto.FullyQualifiedStackName("myOrgOrUser", projectName, stackName)

		// create or select a stack matching the specified name and project.
		// this will set up a workspace with everything necessary to run our inline program (deployFunc)
		s, err := auto.UpsertStackInlineSource(ctx, stackName, projectName, pulumiProgram)
		if err != nil {
			fmt.Printf("Failed to get stack: %v\n", err)
			os.Exit(1)
		}

		logChannel <- logMessage{msg: fmt.Sprintf("Created/Selected stack %q\n", stackName)}

		w := s.Workspace()

		logChannel <- logMessage{msg: fmt.Sprintf("Installing the AWS plugin")}

		// for inline source programs, we must manage plugins ourselves
		err = w.InstallPlugin(ctx, "aws", "v3.2.1")
		if err != nil {
			fmt.Printf("Failed to install program plugins: %v\n", err)
			os.Exit(1)
		}

		logChannel <- logMessage{msg: fmt.Sprintf("Successfully installed AWS plugin")}

		// set stack configuration specifying the AWS region to deploy
		err = s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: "us-west-2"})

		logChannel <- logMessage{msg: fmt.Sprintf("Successfully set config")}
		logChannel <- logMessage{msg: fmt.Sprintf("Running refresh...")}

		_, err = s.Refresh(ctx)
		if err != nil {
			fmt.Printf("Failed to refresh stack: %v\n", err)
			os.Exit(1)
		}

		logChannel <- logMessage{msg: fmt.Sprintf("Refresh succeeded!")}

		if destroy {
			logChannel <- logMessage{msg: fmt.Sprintf("Running destroy...")}

			// destroy our stack and exit early
			_, err := s.Destroy(ctx, optdestroy.EventStreams(eventChannel))
			if err != nil {
				fmt.Printf("Failed to destroy stack: %v", err)
			}
			logChannel <- logMessage{msg: fmt.Sprintf("Stack successfully destroyed")}
			return logMessage{msg: "Success"}
		}

		logChannel <- logMessage{msg: fmt.Sprintf("Running update...")}

		// run the update to deploy our s3 website
		res, err := s.Up(ctx, optup.EventStreams(eventChannel))
		if err != nil {
			fmt.Printf("Failed to update stack: %v\n\n", err)
			os.Exit(1)
		}

		logChannel <- logMessage{msg: fmt.Sprintf("Update succeeded!")}

		// get the URL from the stack outputs
		url, ok := res.Outputs["websiteUrl"].Value.(string)
		if !ok {
			fmt.Println("Failed to unmarshal output URL")
			os.Exit(1)
		}

		logChannel <- logMessage{msg: fmt.Sprintf("URL: %s\n", url)}
		return logMessage{msg: "Success"}
	}
}

// watchForLogMessages forwards any log messages to the `Update` method
func watchForLogMessages(msg chan logMessage) tea.Cmd {
	return func() tea.Msg {
		return <-msg
	}
}

// watchForEvents forwards any engine events to the `Update` method
func watchForEvents(event chan events.EngineEvent) tea.Cmd {
	return func() tea.Msg {
		return <-event
	}
}

type logMessage struct {
	msg string
}

// model is the struct that holds the state for this program
type model struct {
	eventChannel      chan events.EngineEvent // where we'll receive engine events
	logChannel        chan logMessage         // where we'll receive log messages
	spinner           spinner.Model
	destroy           bool
	quitting          bool
	currentMessage    string
	updatesInProgress map[string]string // resources with updates in progress
	updatesComplete   map[string]string // resources with updates completed
}

// Init runs any IO needed at the initialization of the program
func (m model) Init() tea.Cmd {
	return tea.Batch(
		watchForLogMessages(m.logChannel),
		runPulumiUpdate(m.destroy, m.logChannel, m.eventChannel),
		watchForEvents(m.eventChannel),
		spinner.Tick,
	)
}

// Update acts on any events and updates state (model) accordingly
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case events.EngineEvent:
		if msg.ResourcePreEvent != nil {
			m.updatesInProgress[msg.ResourcePreEvent.Metadata.URN] = msg.ResourcePreEvent.Metadata.Type
		}
		if msg.ResOutputsEvent != nil {
			urn := msg.ResOutputsEvent.Metadata.URN
			m.updatesComplete[urn] = msg.ResOutputsEvent.Metadata.Type
			delete(m.updatesInProgress, urn)
		}
		return m, watchForEvents(m.eventChannel) // wait for next event
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		m.quitting = true
		return m, tea.Quit
	case logMessage:
		if msg.msg == "Success" {
			m.currentMessage = "Succeeded!"
			return m, tea.Quit
		}
		m.currentMessage = msg.msg
		return m, watchForLogMessages(m.logChannel)
	default:
		return m, nil
	}
}

// View displays the state in the terminal
func (m model) View() string {
	inProgressText := ""
	completedText := ""
	if len(m.updatesInProgress) > 0 || len(m.updatesComplete) > 0 {
		var inProgVals []string
		for _, v := range m.updatesInProgress {
			inProgVals = append(inProgVals, v)
		}
		sort.Strings(inProgVals)
		inProgressText = fmt.Sprintf("\n\nUpdate in progress: [%s]", strings.Join(inProgVals, ", "))

		var completedVals []string
		for _, v := range m.updatesComplete {
			completedVals = append(completedVals, v)
		}
		sort.Strings(completedVals)
		completedText = fmt.Sprintf("\n\nUpdate complete: [%s]", strings.Join(completedVals, ", "))
	}

	s := fmt.Sprintf("\n%sCurrent step: %s%s%s\n", m.spinner.View(), m.currentMessage, inProgressText, completedText)
	if m.quitting {
		s += "\n"
	}
	return s
}

func main() {
	// to destroy our program, we can run `go run main.go destroy`
	destroy := false
	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) > 0 {
		if argsWithoutProg[0] == "destroy" {
			destroy = true
		}
	}

	s := spinner.NewModel()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	p := tea.NewProgram(model{
		logChannel:        make(chan logMessage),
		eventChannel:      make(chan events.EngineEvent),
		destroy:           destroy,
		spinner:           s,
		updatesInProgress: map[string]string{},
		updatesComplete:   map[string]string{},
	})

	if p.Start() != nil {
		fmt.Println("could not start program")
		os.Exit(1)
	}
}
