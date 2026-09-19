package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Item struct {
	Kind, ID, Name, State, Image, Status, Health string
	CPU                                          float64
	Ports                                        []string
	Details                                      map[string]any
}

func valueText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		encoded, _ := json.MarshalIndent(v, "", "  ")
		return string(encoded)
	}
}

func firstName(value any) string {
	if values, ok := value.([]any); ok && len(values) > 0 {
		return fmt.Sprint(values[0])
	}
	if values, ok := value.([]any); ok && len(values) == 0 {
		return ""
	}
	if value == nil {
		return ""
	}
	return valueText(value)
}

func numberValue(value any) float64 {
	if text, ok := value.(string); ok {
		text = strings.TrimSuffix(strings.TrimSpace(text), "%")
		value = text
	}
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case json.Number:
		result, _ := v.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(v, 64)
		return result
	default:
		return 0
	}
}

func sizeText(value any) string {
	size := numberValue(value)
	for _, unit := range []string{"B", "KiB", "MiB", "GiB", "TiB"} {
		if math.Abs(size) < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", size, unit)
		}
		size /= 1024
	}
	return ""
}

func bytesText(value any) string {
	result := sizeText(value)
	if result == "" {
		return "0 B"
	}
	return result
}

func pythonRound(value float64) int {
	base := math.Floor(value)
	fraction := value - base
	if fraction > 0.5 || (fraction == 0.5 && int(base)%2 != 0) {
		base++
	}
	return int(base)
}

func healthStatus(raw map[string]any, fallback string) string {
	if raw == nil {
		return fallback
	}
	candidates := []any{raw["Health"], raw["Healthcheck"]}
	if state, ok := raw["State"].(map[string]any); ok {
		candidates = append(candidates, state["Health"], state["Healthcheck"], state["HealthStatus"])
	}
	for _, candidate := range candidates {
		if text, ok := candidate.(string); ok && strings.TrimSpace(text) != "" {
			return strings.ToLower(strings.TrimSpace(text))
		}
		if object, ok := candidate.(map[string]any); ok {
			if status := object["Status"]; status != nil {
				return strings.ToLower(strings.TrimSpace(fmt.Sprint(status)))
			}
			if status := object["status"]; status != nil {
				return strings.ToLower(strings.TrimSpace(fmt.Sprint(status)))
			}
		}
	}
	if config, ok := raw["Config"].(map[string]any); ok {
		if _, present := config["Healthcheck"]; present {
			if config["Healthcheck"] == nil {
				return "no healthcheck"
			}
			return "not started"
		}
	}
	return fallback
}

func portMappings(raw map[string]any) []string {
	values := raw["Ports"]
	if values == nil {
		values = raw["PortMappings"]
	}
	if object, ok := values.(map[string]any); ok {
		values = []any{object}
	}
	list, ok := values.([]any)
	if !ok {
		return nil
	}
	result := []string{}
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			if text := strings.TrimSpace(valueText(entry)); text != "" {
				result = append(result, text)
			}
			continue
		}
		containerPort := object["container_port"]
		if containerPort == nil {
			containerPort = object["containerPort"]
		}
		if containerPort == nil {
			containerPort = object["container_port_num"]
		}
		hostPort := object["host_port"]
		if hostPort == nil {
			hostPort = object["hostPort"]
		}
		protocol := fmt.Sprint(object["protocol"])
		if protocol == "<nil>" || protocol == "" {
			protocol = "tcp"
		}
		hostIP := fmt.Sprint(object["host_ip"])
		if hostIP == "<nil>" || hostIP == "" {
			hostIP = fmt.Sprint(object["hostIP"])
		}
		if hostIP == "<nil>" || hostIP == "" {
			hostIP = "0.0.0.0"
		}
		if hostPort != nil && numberValue(hostPort) != 0 {
			result = append(result, fmt.Sprintf("%s:%s->%s/%s", hostIP, scalarText(hostPort), scalarText(containerPort), protocol))
		} else if containerPort != nil {
			result = append(result, fmt.Sprintf("%s/%s", scalarText(containerPort), protocol))
		}
	}
	return result
}

func scalarText(value any) string {
	if value == nil {
		return ""
	}
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return fmt.Sprint(value)
}

func containerItem(raw map[string]any) Item {
	id := scalarText(raw["Id"])
	if id == "" {
		id = scalarText(raw["ID"])
	}
	name := strings.TrimPrefix(firstName(raw["Names"]), "/")
	if name == "" {
		name = scalarText(raw["Name"])
	}
	if name == "" {
		name = shortID(id)
	}
	return Item{Kind: "container", ID: id, Name: name, State: defaultText(raw["State"], "unknown"), Image: scalarText(raw["Image"]), Status: scalarText(raw["Status"]), Health: healthStatus(raw, "unknown"), Ports: portMappings(raw)}
}

func podItem(raw map[string]any) Item {
	id := scalarText(raw["Id"])
	if id == "" {
		id = scalarText(raw["ID"])
	}
	count := 0
	if containers, ok := raw["Containers"].([]any); ok {
		count = len(containers)
	}
	return Item{Kind: "pod", ID: id, Name: defaultText(raw["Name"], shortID(id)), State: defaultText(raw["Status"], "unknown"), Status: fmt.Sprintf("%d containers", count)}
}

func imageItem(raw map[string]any) Item {
	id := defaultText(raw["Id"], defaultText(raw["ID"], ""))
	name := firstName(raw["RepoTags"])
	if name == "" {
		name = shortID(id)
	}
	return Item{Kind: "image", ID: id, Name: name, State: "image", Status: sizeText(raw["Size"]), Details: raw}
}

func volumeItem(raw map[string]any) Item {
	name := scalarText(raw["Name"])
	return Item{Kind: "volume", ID: name, Name: name, State: "volume", Status: defaultText(raw["Driver"], "local"), Details: raw}
}

func networkItem(raw map[string]any) Item {
	name := defaultText(raw["Name"], raw["name"])
	driver := defaultText(raw["Driver"], raw["driver"])
	if driver == "" {
		driver = "bridge"
	}
	return Item{Kind: "network", ID: name, Name: name, State: "network", Status: driver, Details: raw}
}

func secretItem(raw map[string]any) Item {
	id := defaultText(raw["ID"], raw["Id"])
	name := defaultText(raw["Name"], raw["name"])
	if name == "" {
		name = shortID(id)
	}
	driver := "file"
	if spec, ok := raw["Spec"].(map[string]any); ok {
		driver = defaultText(spec["Driver"], driver)
		if driverObject, ok := spec["Driver"].(map[string]any); ok {
			driver = defaultText(driverObject["Name"], driver)
		}
	}
	return Item{Kind: "secret", ID: id, Name: name, State: "secret", Status: driver, Details: raw}
}

func defaultText(value any, fallback any) string {
	text := scalarText(value)
	if text == "" || text == "<nil>" {
		return scalarText(fallback)
	}
	return text
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func containerStateLabel(item Item) string {
	state := strings.ToLower(item.State)
	if state == "exited" && strings.HasPrefix(item.Status, "Exited (") {
		if end := strings.Index(item.Status, ")"); end >= 0 {
			return strings.ToLower(item.Status[:end+1])
		}
	}
	return item.State
}

func statsPayload(value any) map[string]any {
	if list, ok := value.([]any); ok {
		if len(list) == 0 {
			return nil
		}
		value = list[0]
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if list, ok := object["Stats"].([]any); ok {
		if len(list) == 0 {
			return nil
		}
		object, _ = list[0].(map[string]any)
	}
	return object
}

var sparkChars = []rune("▁▂▃▄▅▆▇█")

func gauge(value float64, width int) string {
	if width <= 0 {
		return ""
	}
	ratio := value / 100
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := pythonRound(ratio * float64(width))
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func sparkline(values []float64, width int) string {
	if len(values) == 0 {
		return "(no samples)"
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	low, high := values[0], values[0]
	for _, value := range values[1:] {
		if value < low {
			low = value
		}
		if value > high {
			high = value
		}
	}
	if high == low {
		index := 0
		if high > 0 {
			index = len(sparkChars) / 2
		}
		return strings.Repeat(string(sparkChars[index]), len(values))
	}
	var result strings.Builder
	for _, value := range values {
		index := pythonRound((value - low) / (high - low) * float64(len(sparkChars)-1))
		result.WriteRune(sparkChars[index])
	}
	return result.String()
}

func statsLines(history []map[string]any) []string {
	if len(history) == 0 {
		return []string{"No statistics available."}
	}
	valueFor := func(sample map[string]any, key, fallback string) float64 {
		value := sample[key]
		if value == nil {
			value = sample[fallback]
		}
		return numberValue(value)
	}
	latest := history[len(history)-1]
	cpuValues, memoryValues := []float64{}, []float64{}
	for _, sample := range history {
		cpuValues = append(cpuValues, valueFor(sample, "CPU", "AvgCPU"))
		memoryValues = append(memoryValues, valueFor(sample, "MemPerc", ""))
	}
	cpu, memory := cpuValues[len(cpuValues)-1], memoryValues[len(memoryValues)-1]
	return []string{
		"Live container statistics", "",
		fmt.Sprintf("CPU       %6.2f%%  [%s]", cpu, gauge(cpu, 20)),
		"          " + sparkline(cpuValues, 32),
		fmt.Sprintf("Memory    %6.2f%%  [%s]", memory, gauge(memory, 20)),
		"          " + sparkline(memoryValues, 32),
		fmt.Sprintf("Usage     %s / %s", bytesText(latest["MemUsage"]), bytesText(latest["MemLimit"])),
		fmt.Sprintf("PIDs      %d", int(numberValue(latest["PIDs"]))),
		"Block I/O in   " + bytesText(latest["BlockInput"]),
		"Block I/O out  " + bytesText(latest["BlockOutput"]), "",
		fmt.Sprintf("Samples: %d  ·  refresh interval: %gs", len(history), refreshInterval.Seconds()),
	}
}
