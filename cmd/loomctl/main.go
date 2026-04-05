package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/rcarson/loom/internal/client"
)

var (
	outputJSON bool
	cfgPath    string
)

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "loom-config.yaml"
	}
	return filepath.Join(home, ".config", "loom", "config.yaml")
}

func main() {
	root := &cobra.Command{
		Use:   "loomctl",
		Short: "Control plane CLI for Loom",
	}

	root.PersistentFlags().StringVar(&cfgPath, "config", defaultConfigPath(), "path to loomctl config")
	root.PersistentFlags().BoolVarP(&outputJSON, "output-json", "j", false, "output as JSON")

	root.AddCommand(contextCmd(), nodeCmd(), jobCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func loadClient() (*client.Client, error) {
	cfg, err := client.LoadConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	return client.New(cfg, cfgPath), nil
}

// --- context ---

func contextCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "context", Short: "Manage server contexts"}

	cmd.AddCommand(&cobra.Command{
		Use:   "use <name>",
		Short: "Set the current context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			if err := c.UseContext(args[0]); err != nil {
				return err
			}
			fmt.Printf("Switched to context %q\n", args[0])
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			cfg, err := client.LoadConfig(cfgPath)
			if err != nil {
				return err
			}
			contexts := c.ListContexts()
			if outputJSON {
				return printJSON(contexts)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CURRENT\tNAME\tSERVER")
			for _, ctx := range contexts {
				current := ""
				if ctx.Name == cfg.CurrentContext {
					current = "*"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", current, ctx.Name, ctx.Server)
			}
			return w.Flush()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "current",
		Short: "Print the current context name",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			ctx, err := c.CurrentContext()
			if err != nil {
				return err
			}
			fmt.Println(ctx.Name)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "add <name> <server-url>",
		Short: "Add or update a context",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			token, _ := cmd.Flags().GetString("token")
			c, err := loadClient()
			if err != nil {
				return err
			}
			if err := c.AddContext(client.Context{Name: args[0], Server: args[1], Token: token}); err != nil {
				return err
			}
			fmt.Printf("Context %q added\n", args[0])
			return nil
		},
	})

	return cmd
}

// --- node ---

func nodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "node", Short: "Manage nodes"}

	cmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			nodes, err := c.ListNodes()
			if err != nil {
				return err
			}
			if outputJSON {
				return printJSON(nodes)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tREGION\tZONE\tTAGS\tSTATUS\tLAST HEARTBEAT")
			for _, n := range nodes {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					n.Name, n.Region, n.Zone, n.Tags, n.Status,
					n.LastHeartbeat.Format("2006-01-02 15:04:05"))
			}
			return w.Flush()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "inspect <name>",
		Short: "Show node details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			n, err := c.GetNode(args[0])
			if err != nil {
				return err
			}
			return printJSON(n)
		},
	})

	return cmd
}

// --- job ---

func jobCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "job", Short: "Manage jobs"}

	cmd.AddCommand(&cobra.Command{
		Use:   "run <file.yaml>",
		Short: "Submit or update a job from a spec file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read %s: %w", args[0], err)
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			if err := c.SubmitJob(string(data)); err != nil {
				return err
			}
			fmt.Println("Job submitted.")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			jobs, err := c.ListJobs()
			if err != nil {
				return err
			}
			if outputJSON {
				return printJSON(jobs)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tTYPE\tPLACEMENTS\tUPDATED")
			for _, j := range jobs {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n",
					j.Name, j.Type, len(j.Placements),
					j.UpdatedAt.Format("2006-01-02 15:04:05"))
			}
			return w.Flush()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "status <name>",
		Short: "Show job status and placements",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			j, err := c.GetJob(args[0])
			if err != nil {
				return err
			}
			if outputJSON {
				return printJSON(j)
			}
			fmt.Printf("Name:    %s\n", j.Name)
			fmt.Printf("Type:    %s\n", j.Type)
			fmt.Printf("Updated: %s\n\n", j.UpdatedAt.Format("2006-01-02 15:04:05"))
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PLACEMENT ID\tNODE\tCONTAINER ID\tSTATUS")
			for _, p := range j.Placements {
				cid := p.ContainerID
				if len(cid) > 12 {
					cid = cid[:12]
				}
				if cid == "" {
					cid = "-"
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", p.ID, p.NodeName, cid, p.Status)
			}
			return w.Flush()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stop <name>",
		Short: "Stop and remove a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			if err := c.DeleteJob(args[0]); err != nil {
				return err
			}
			fmt.Printf("Job %q stopped.\n", args[0])
			return nil
		},
	})

	return cmd
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
