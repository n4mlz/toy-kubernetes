package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
	"toy-kubernetes/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: toyctl <apply|get|describe|delete>")
	}

	flags := flag.NewFlagSet("toyctl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		return err
	}
	client := apiserver.NewClient(config.APIServerURL)
	commandArgs := flags.Args()
	if len(commandArgs) == 0 {
		return errors.New("command is required")
	}

	switch commandArgs[0] {
	case "apply":
		return apply(context.Background(), client, commandArgs[1:])
	case "get":
		return get(context.Background(), client, commandArgs[1:])
	case "describe":
		return describe(context.Background(), client, commandArgs[1:])
	case "delete":
		return deleteResource(context.Background(), client, commandArgs[1:])
	default:
		return fmt.Errorf("unknown command %q", commandArgs[0])
	}
}

func apply(ctx context.Context, client *apiserver.Client, args []string) error {
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("f", "", "manifest file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("apply requires -f FILE")
	}

	manifest, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer manifest.Close()

	decoder := yaml.NewDecoder(manifest)
	for document := 1; ; document++ {
		var value map[string]any
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("decode document %d: %w", document, err)
		}
		if len(value) == 0 {
			continue
		}

		object, err := decodeManifest(value)
		if err != nil {
			return fmt.Errorf("document %d: %w", document, err)
		}
		created, err := applyResource(ctx, client, object)
		if err != nil {
			return err
		}
		verb := "configured"
		if created {
			verb = "created"
		}
		fmt.Printf("%s/%s %s\n", resourceKind(object), object.GetName(), verb)
	}
}

func decodeManifest(value map[string]any) (api.Resource, error) {
	kind, ok := value["kind"].(string)
	if !ok || kind == "" {
		return nil, errors.New("kind is required")
	}

	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}

	var object api.Resource
	switch kind {
	case "Node":
		object = &api.Node{}
	case "Pod":
		object = &api.Pod{}
	case "Deployment":
		object = &api.Deployment{}
	case "ReplicaSet":
		object = &api.ReplicaSet{}
	case "Service":
		object = &api.Service{}
	default:
		return nil, fmt.Errorf("unsupported kind %q", kind)
	}
	if err := yaml.Unmarshal(data, object); err != nil {
		return nil, err
	}
	if object.GetName() == "" {
		return nil, errors.New("metadata.name is required")
	}
	return object, nil
}

func applyResource(ctx context.Context, client *apiserver.Client, object api.Resource) (bool, error) {
	switch object := object.(type) {
	case *api.Node:
		current, err := client.Nodes().Get(ctx, object.Name)
		if err == nil {
			object.Status = current.Status
			object.ResourceVersion = current.ResourceVersion
			_, err = client.Nodes().Update(ctx, object.Name, *object)
			return false, err
		}
		_, err = client.Nodes().Create(ctx, *object)
		return true, err
	case *api.Pod:
		current, err := client.Pods().Get(ctx, object.Name)
		if err == nil {
			if object.Spec.NodeName == "" {
				object.Spec.NodeName = current.Spec.NodeName
			}
			object.Status = current.Status
			object.ResourceVersion = current.ResourceVersion
			_, err = client.Pods().Update(ctx, object.Name, *object)
			return false, err
		}
		_, err = client.Pods().Create(ctx, *object)
		return true, err
	case *api.Deployment:
		current, err := client.Deployments().Get(ctx, object.Name)
		if err == nil {
			object.Status = current.Status
			object.ResourceVersion = current.ResourceVersion
			_, err = client.Deployments().Update(ctx, object.Name, *object)
			return false, err
		}
		_, err = client.Deployments().Create(ctx, *object)
		return true, err
	case *api.ReplicaSet:
		current, err := client.ReplicaSets().Get(ctx, object.Name)
		if err == nil {
			object.Status = current.Status
			object.ResourceVersion = current.ResourceVersion
			_, err = client.ReplicaSets().Update(ctx, object.Name, *object)
			return false, err
		}
		_, err = client.ReplicaSets().Create(ctx, *object)
		return true, err
	case *api.Service:
		current, err := client.Services().Get(ctx, object.Name)
		if err == nil {
			if object.Spec.ClusterIP == "" {
				object.Spec.ClusterIP = current.Spec.ClusterIP
			}
			if object.Spec.NodePort == 0 {
				object.Spec.NodePort = current.Spec.NodePort
			}
			object.ResourceVersion = current.ResourceVersion
			_, err = client.Services().Update(ctx, object.Name, *object)
			return false, err
		}
		_, err = client.Services().Create(ctx, *object)
		return true, err
	default:
		return false, fmt.Errorf("unsupported resource %T", object)
	}
}

func resourceKind(object api.Resource) string {
	switch object.(type) {
	case *api.Node:
		return "Node"
	case *api.Pod:
		return "Pod"
	case *api.Deployment:
		return "Deployment"
	case *api.ReplicaSet:
		return "ReplicaSet"
	case *api.Service:
		return "Service"
	default:
		return "Resource"
	}
}

func get(ctx context.Context, client *apiserver.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("get requires RESOURCE/NAME")
	}
	kind, name, err := splitResource(args[0])
	if err != nil {
		return err
	}
	object, err := getResource(ctx, client, kind, name)
	if err != nil {
		return err
	}
	fmt.Printf("%s/%s\n", kind, object.GetName())
	return nil
}

func describe(ctx context.Context, client *apiserver.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("describe requires RESOURCE/NAME")
	}
	kind, name, err := splitResource(args[0])
	if err != nil {
		return err
	}
	object, err := getResource(ctx, client, kind, name)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(object)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

func deleteResource(ctx context.Context, client *apiserver.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("delete requires RESOURCE/NAME")
	}
	kind, name, err := splitResource(args[0])
	if err != nil {
		return err
	}
	object, err := getResource(ctx, client, kind, name)
	if err != nil {
		return err
	}
	if err := deleteObject(ctx, client, kind, name, object.GetResourceVersion()); err != nil {
		return err
	}
	fmt.Printf("%s/%s deleted\n", kind, name)
	return nil
}

func getResource(ctx context.Context, client *apiserver.Client, kind, name string) (api.Resource, error) {
	switch kind {
	case "Node":
		object, err := client.Nodes().Get(ctx, name)
		return &object, err
	case "Pod":
		object, err := client.Pods().Get(ctx, name)
		return &object, err
	case "Deployment":
		object, err := client.Deployments().Get(ctx, name)
		return &object, err
	case "ReplicaSet":
		object, err := client.ReplicaSets().Get(ctx, name)
		return &object, err
	case "Service":
		object, err := client.Services().Get(ctx, name)
		return &object, err
	default:
		return nil, fmt.Errorf("unsupported kind %q", kind)
	}
}

func deleteObject(ctx context.Context, client *apiserver.Client, kind, name string, version int64) error {
	switch kind {
	case "Node":
		return client.Nodes().Delete(ctx, name, version)
	case "Pod":
		return client.Pods().Delete(ctx, name, version)
	case "Deployment":
		return client.Deployments().Delete(ctx, name, version)
	case "ReplicaSet":
		return client.ReplicaSets().Delete(ctx, name, version)
	case "Service":
		return client.Services().Delete(ctx, name, version)
	default:
		return fmt.Errorf("unsupported kind %q", kind)
	}
}

func splitResource(value string) (string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("resource must be RESOURCE/NAME")
	}
	kind := map[string]string{"nodes": "Node", "pods": "Pod", "deployments": "Deployment", "replicasets": "ReplicaSet", "services": "Service"}[strings.ToLower(parts[0])]
	if kind == "" {
		return "", "", fmt.Errorf("unsupported resource %q", parts[0])
	}
	return kind, parts[1], nil
}
