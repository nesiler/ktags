package cli

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nesiler/ktags/internal/service"
)

const customerUsage = `usage: ktags customer <command> [--json]

Customers are the directories of the inventory in the ktags data root. A record that cannot
be read is listed with its problem and does not hide the others.

commands:
  list    list every customer with environment, cluster and node count

--json data: list {"customers":[{"id","name","environment","cluster","nodes","problem"}]}

next: ktags action list
`

type customerListDoc struct {
	Customers []service.CustomerInfo `json:"customers"`
}

func (e *env) customerCmd() *cobra.Command {
	return group("customer", customerUsage, &cobra.Command{
		Use:  "list",
		Long: "usage: ktags customer list [--json]\n\n" + customerUsage,
		Args: positional(),
		RunE: func(*cobra.Command, []string) error { return e.customerList() },
	})
}

func (e *env) customerList() error {
	client, err := e.client()
	if err != nil {
		return err
	}
	customers, err := client.Customers(e.ctx)
	if err != nil {
		return err
	}
	w := e.text()
	_, _ = fmt.Fprintf(w, "%d customer(s)\n", len(customers))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  CUSTOMER\tENVIRONMENT\tCLUSTER\tNODES\tRECORD")
	for _, c := range customers {
		record, nodes := "ok", strconv.Itoa(c.Nodes)
		if c.Problem != "" {
			record, nodes = "refused: "+c.Problem, "-"
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", c.ID, dash(c.Environment), dash(c.Cluster), nodes, record)
	}
	_ = tw.Flush()
	return e.emit("customer.list", customerListDoc{Customers: nonNil(customers)})
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
