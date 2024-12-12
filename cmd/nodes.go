/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	client "github.com/akomic/kubectl-xtop/client"
	"github.com/spf13/cobra"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// nodesCmd represents the nodes command
var (
	sortBy string
)

var nodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "Top nodes",
	Run: func(cmd *cobra.Command, args []string) {
		runNodesCommand()
	},
}

type column struct {
	header string
	getter func(nodeInfo) string
}

type nodeInfo struct {
	name      string
	resources map[string]*resource.Quantity
}

func toColumnName(key string) string {
	// Convert camelCase key to uppercase spaced format
	// e.g.: "cpuCapacity" -> "CPU CAPACITY", "diskCapacity" -> "DISK CAPACITY"
	for i := 1; i < len(key); i++ {
		if key[i] >= 'A' && key[i] <= 'Z' {
			return strings.ToUpper(key[:i]) + " " + strings.ToUpper(key[i:])
		}
	}
	return strings.ToUpper(key) // fallback for keys without camelCase
}

var columns []column

type nodeInfoList []nodeInfo

func (n nodeInfoList) Len() int      { return len(n) }
func (n nodeInfoList) Swap(i, j int) { n[i], n[j] = n[j], n[i] }
func (n nodeInfoList) Less(i, j int) bool {
	switch sortBy {
	case "cpu-req":
		return n[i].resources["cpuReq"].Cmp(*n[j].resources["cpuReq"]) < 0
	case "cpu-limit":
		return n[i].resources["cpuLimit"].Cmp(*n[j].resources["cpuLimit"]) < 0
	case "mem-req":
		return n[i].resources["memReq"].Cmp(*n[j].resources["memReq"]) < 0
	case "mem-limit":
		return n[i].resources["memLimit"].Cmp(*n[j].resources["memLimit"]) < 0
	default: // "name"
		return n[i].name < n[j].name
	}
}

func runNodesCommand() {
	// Initialize maps outside loop
	nodesMeta := make(map[string]map[string]string)
	nodesResources := make(map[string]map[string]*resource.Quantity)

	// Get nodes info
	nodes, err := client.Clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	// Initialize node resources
	for _, node := range nodes.Items {
		nodesMeta[node.ObjectMeta.Name] = map[string]string{
			"arch": node.ObjectMeta.Labels["kubernetes.io/arch"],
			"os":   node.ObjectMeta.Labels["kubernetes.io/os"],
			"type": node.ObjectMeta.Labels["node.kubernetes.io/instance-type"],
		}
		nodesResources[node.ObjectMeta.Name] = map[string]*resource.Quantity{
			"cpuReq":      resource.NewQuantity(0, resource.DecimalSI),
			"cpuLimit":    resource.NewQuantity(0, resource.DecimalSI),
			"cpuCapacity": node.Status.Capacity.Cpu(),
			"memReq":      resource.NewQuantity(0, resource.BinarySI),
			"memLimit":    resource.NewQuantity(0, resource.BinarySI),
			"memCapacity": node.Status.Capacity.Memory(),
		}
	}

	// Get and process pods
	pods, err := client.Clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	// Calculate resource usage
	for _, pod := range pods.Items {
		nodeName := pod.Spec.NodeName
		if _, exists := nodesResources[nodeName]; !exists {
			continue // Skip pods on unknown nodes
		}

		for _, container := range pod.Spec.Containers {
			if container.Resources.Requests != nil {
				if cpu := container.Resources.Requests.Cpu(); cpu != nil {
					nodesResources[nodeName]["cpuReq"].Add(*cpu)
				} else if debug {
					fmt.Printf("DEBUG: Pod %s/%s container %s has nil CPU request\n",
						pod.Namespace, pod.Name, container.Name)
				}
				if memory := container.Resources.Requests.Memory(); memory != nil {
					nodesResources[nodeName]["memReq"].Add(*memory)
				} else if debug {
					fmt.Printf("DEBUG: Pod %s/%s container %s has nil memory request\n",
						pod.Namespace, pod.Name, container.Name)
				}
			} else if debug {
				fmt.Printf("DEBUG: Pod %s/%s container %s has nil requests\n",
					pod.Namespace, pod.Name, container.Name)
			}

			if container.Resources.Limits != nil {
				if cpu := container.Resources.Limits.Cpu(); cpu != nil {
					nodesResources[nodeName]["cpuLimit"].Add(*cpu)
				} else if debug {
					fmt.Printf("DEBUG: Pod %s/%s container %s has nil CPU limit\n",
						pod.Namespace, pod.Name, container.Name)
				}
				if memory := container.Resources.Limits.Memory(); memory != nil {
					nodesResources[nodeName]["memLimit"].Add(*memory)
				} else if debug {
					fmt.Printf("DEBUG: Pod %s/%s container %s has nil memory limit\n",
						pod.Namespace, pod.Name, container.Name)
				}
			} else if debug {
				fmt.Printf("DEBUG: Pod %s/%s container %s has nil limits\n",
					pod.Namespace, pod.Name, container.Name)
			}
		}
	}

	// Convert map to sortable slice
	nodesList := make(nodeInfoList, 0, len(nodesResources))
	for nodeName, resources := range nodesResources {
		nodesList = append(nodesList, nodeInfo{
			name:      nodeName,
			resources: resources,
		})
	}

	// Sort the slice
	sort.Sort(nodesList)

	// Print results
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.TabIndent)

	// Print headers
	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = col.header
	}
	fmt.Fprintln(w, strings.Join(headers, " \t ")+" \t")

	// Print rows
	for _, node := range nodesList {
		values := make([]string, len(columns))
		for i, col := range columns {
			values[i] = col.getter(node)
		}
		fmt.Fprintln(w, strings.Join(values, " \t ")+" \t")
	}
	w.Flush()
}

func init() {
	// Initialize base columns
	columns = []column{
		{
			header: "NAME",
			getter: func(node nodeInfo) string {
				return node.name
			},
		},
	}

	// Add resource columns dynamically
	resourceKeys := []string{"cpuCapacity", "cpuReq", "cpuLimit", "memCapacity", "memReq", "memLimit"}
	for _, key := range resourceKeys {
		col := column{
			header: toColumnName(key),
			getter: func(key string) func(node nodeInfo) string {
				return func(node nodeInfo) string {
					val, suffix := node.resources[key].CanonicalizeBytes(make([]byte, 0, 100))
					if strings.HasSuffix(key, "Req") {
						capacity := node.resources[strings.TrimSuffix(key, "Req")+"Capacity"]
						percentage := (float64(node.resources[key].Value()) / float64(capacity.Value())) * 100
						return fmt.Sprintf("%s%s (%.2f%%)", string(val), string(suffix), percentage)
					}
					return string(val) + string(suffix)
				}
			}(key),
		}
		columns = append(columns, col)
	}

	rootCmd.AddCommand(nodesCmd)
	nodesCmd.Flags().StringVar(&sortBy, "sort-by", "name", "Sort nodes by: name, cpu-req, cpu-limit, mem-req, mem-limit")
}
