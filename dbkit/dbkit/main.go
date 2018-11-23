package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/oliverkofoed/gokit/dbkit"
	"github.com/spf13/cobra"
)

var extrafields []string
var logging bool

func Main() {
	main()
}

func main() {
	// build corbra-command tree
	DbkitCmd.AddCommand(DbkitGenerateCommand)

	DbkitGenerateCommand.PersistentFlags().StringArrayVar(&extrafields, "extrafields", []string{}, "path to an extrafields file")
	DbkitGenerateCommand.PersistentFlags().BoolVar(&logging, "logging", false, "sql query logging")

	// run dogo
	DbkitCmd.SilenceErrors = true
	DbkitCmd.SilenceUsage = true
	if err := DbkitCmd.Execute(); err != nil {
		if merr, ok := err.(*multiErr); ok {
			for _, err := range merr.errors {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err.Error())
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err.Error())
		}
		os.Exit(-1)
	}
}

// DbkitCmd represents the base command when called without any subcommands
var DbkitCmd = &cobra.Command{
	Use:   "dbkit",
	Short: "A simple tool to generate code from databases",
}

// DbkitGenerateCommand represents the 'dbkit generate' command
var DbkitGenerateCommand = &cobra.Command{
	Use:     "generate",
	Short:   "Reads the schema from the given database and generates the go-code to access it with dbkit",
	Example: "dbkit generate pgsql://user@host.com packagename foldername generators...",
	RunE: func(cmd *cobra.Command, args []string) error {
		dbUrl := args[0]
		packageName := args[1]
		dir := args[2]

		// get a db connection
		u, err := url.Parse(dbUrl)
		if err != nil {
			return err
		}

		switch u.Scheme {
		case "postgres":
			p, err := dbkit.OpenPostgres(u)
			if err != nil {
				return err
			}

			s, err := p.GetSchema(packageName, func(msg string, args ...interface{}) {
				fmt.Printf(msg+"\n", args...)
			})
			if err != nil {
				return err
			}

			for _, filename := range extrafields {
				err := s.ReadExtraFieldsFile(filename, func(msg string, args ...interface{}) {
					fmt.Printf(msg+"\n", args...)
				})
				if err != nil {
					return err
				}
			}

			errs := s.Generate(dir, logging, "postgres")
			if len(errs) > 0 {
				return &multiErr{errors: errs}
			}
		case "cassandra":
			p, err := dbkit.OpenCassandra(u)
			if err != nil {
				return err
			}

			s, err := p.GetSchema(packageName, func(msg string, args ...interface{}) {
				fmt.Printf(msg+"\n", args...)
			})
			if err != nil {
				return err
			}

			for _, filename := range extrafields {
				err := s.ReadExtraFieldsFile(filename, func(msg string, args ...interface{}) {
					fmt.Printf(msg+"\n", args...)
				})
				if err != nil {
					return err
				}
			}

			errs := s.Generate(dir, logging, "cassandra")
			if len(errs) > 0 {
				return &multiErr{errors: errs}
			}
		default:
			return errors.New("Unsupported schema: " + u.Scheme)
		}
		return nil
	},
}

type multiErr struct {
	errors []error
}

func (m *multiErr) Error() string {
	return "deconstruct to get actual errors"
}
