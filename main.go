package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	httpstreamspdy "k8s.io/apimachinery/pkg/util/httpstream/spdy"
	apiremotecommand "k8s.io/apimachinery/pkg/util/remotecommand"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Supported OS image types and related configs.
// Each OS type maps to one virtio config and one zone config.

type OSConfig struct {
	VirtioConfig string
	ZoneConfig   string
	ZoneID       string
}

// Mapping from logical image name to OSConfig.
var supportedImages = map[string]OSConfig{
	"hvisor-linux": {
		VirtioConfig: "/root/virtio_cfg.json",
		ZoneConfig:   "/root/zone1-linux.json",
		ZoneID:       "1",
	},
	"hvisor-ruxos": {
		VirtioConfig: "/root/virtio_cfg_ruxos.json",
		ZoneConfig:   "/root/zone1-ruxos.json",
		ZoneID:       "1",
	},
	"hvisor-zephyr": {
		VirtioConfig: "/root/virtio_cfg_zephyr.json",
		ZoneConfig:   "/root/zone1-zephyr.json",
		ZoneID:       "1",
	},
}


// Create VM
func hvisorCreateVM(vmID, name, image string) error {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("Create VM: vmID=%s, name=%s", vmID, name)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return nil
}

// Start VM
func hvisorStartVM(vmID, osType string) error {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("Start VM: vmID=%s (OS Type: %s)", vmID, osType)

	// get corresponding OS config
	config, exists := supportedImages[osType]
	if !exists {
		log.Printf("Not supported OS type: %s", osType)
		return fmt.Errorf("Not supported OS type: %s", osType)
	}

	log.Printf("   use config: virtio=%s, zone=%s, zoneID=%s", config.VirtioConfig, config.ZoneConfig, config.ZoneID)

	// 1. start virtio daemon (background)
	log.Printf("   step 1/2: start virtio daemon...")
	log.Printf("   command: nohup ./hvisor virtio start %s", config.VirtioConfig)
	log.Printf("   working directory: /root")
	
	cmd := exec.Command("nohup", "./hvisor", "virtio", "start", config.VirtioConfig)
	cmd.Dir = "/root"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	// start virtio process
	if err := cmd.Start(); err != nil {
		log.Printf("virtio start failed: %v", err)
		return fmt.Errorf("virtio start failed: %w", err)
	}
	log.Printf("  virtio daemon started (PID: %d)", cmd.Process.Pid)

	// wait for a short time to ensure virtio process started
	time.Sleep(500 * time.Millisecond)

	// 2. start zone
	log.Printf("  step 2/2: start zone...")
	log.Printf("  command: ./hvisor zone start %s", config.ZoneConfig)
	
	cmd = exec.Command("./hvisor", "zone", "start", config.ZoneConfig)
	cmd.Dir = "/root"
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("zone start failed: %v", err)
		log.Printf("output: %s", string(output))
		return fmt.Errorf("zone start failed: %w, output: %s", err, string(output))
	}

	log.Printf("zone started successfully")
	log.Printf("output: %s", string(output))
	log.Printf("VM started successfully (OS Type: %s, zone ID: %s)", osType, config.ZoneID)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return nil
}

// extract OS type from image name
func extractOSType(imageRef string) string {
	if imageRef == "" {
		return ""
	}

	// remove image tag
	if strings.Contains(imageRef, ":") {
		imageRef = strings.Split(imageRef, ":")[0]
	}

	// check if it is directly supported OS type
	if _, exists := supportedImages[imageRef]; exists {
		return imageRef
	}

	// split image name
	parts := strings.Split(imageRef, "/")
	imageName := imageRef
	if len(parts) >= 2 {
		imageName = parts[1]
	}

	// remove image tag
	if strings.Contains(imageName, ":") {
		imageName = strings.Split(imageName, ":")[0]
	}

	// handle new format: hvisor-os
	if strings.HasPrefix(imageName, "hvisor-") {
		osType := imageName
		if _, exists := supportedImages[osType]; exists {
			return osType
		}

		baseType := strings.TrimPrefix(imageName, "hvisor-")
		if _, exists := supportedImages[baseType]; exists {
			return baseType
		}
	}

	// extract OS type
	subParts := strings.Split(imageName, "-")
	if len(subParts) == 0 {
		return ""
	}

	osType := subParts[0]
	// check if it is supported OS type
	if _, exists := supportedImages[osType]; exists {
		return osType
	}

	return ""
}

// stop VM
func hvisorStopVM(vmID, osType string) error {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("Stop VM: vmID=%s, osType=%s", vmID, osType)

	config, exists := supportedImages[osType]
	if !exists {
		log.Printf("Not supported OS type: %s", osType)
		return fmt.Errorf("Not supported OS type: %s", osType)
	}

	log.Printf("use config: zoneID=%s", config.ZoneID)

	// 1. stop zone
	log.Printf("step 1/2: stop zone...")
	log.Printf("command: ./hvisor zone shutdown -id %s", config.ZoneID)
	log.Printf("working directory: /root")
	
	cmd := exec.Command("./hvisor", "zone", "shutdown", "-id", config.ZoneID)
	cmd.Dir = "/root"
	output, err := cmd.CombinedOutput()
	if err != nil {
		// if zone not found or already stopped, ignore error 
		outputStr := string(output)
		if strings.Contains(outputStr, "not found") || 
		   strings.Contains(outputStr, "not running") ||
		   strings.Contains(outputStr, "already stopped") {
			log.Printf("zone may be stopped or not found, ignore error: %s", outputStr)
		} else {
			log.Printf("zone stop failed: %v, output: %s", err, outputStr)
			return fmt.Errorf("zone stop failed: %w, output: %s", err, outputStr)
		}
	} else {
		log.Printf("zone stopped successfully")
		log.Printf("output: %s", string(output))
	}

	log.Printf("VM stopped successfully")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return nil
}

// delete VM
func hvisorDeleteVM(vmID string) error {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("Delete VM: vmID=%s", vmID)

	// extract OS type from vmID
	osType := extractOSType(vmID)
	if osType != "" {
		log.Printf("OS type: %s", osType)
	}

	// delete operation does not need to be actually called, config already exists
	log.Printf("VM deleted successfully")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return nil
}

// CRI service implementation
type ContainerInfo struct {
	ID        string
	Name      string
	PodID     string
	Image     string
	LogPath   string
	State     runtimeapi.ContainerState
	CreatedAt int64
	StartedAt int64
	FinishedAt int64  // container stopped time
	ExitCode   int32  // container exit code
	Reason     string // container status reason
	Message    string // container status message
	Attempt   int32
	Labels    map[string]string
	Annotations map[string]string
	VMStarted bool
	ZoneID    string // VM/Zone ID
}

type SimpleRuntimeService struct {
	containers map[string]*ContainerInfo
}

func (s *SimpleRuntimeService) Version(ctx context.Context, req *runtimeapi.VersionRequest) (*runtimeapi.VersionResponse, error) {
	return &runtimeapi.VersionResponse{
		Version:           "0.1.0",
		RuntimeName:       "hvisor",
		RuntimeVersion:    "0.1.0",
		RuntimeApiVersion: "0.1.0",
	}, nil
}

func (s *SimpleRuntimeService) Status(ctx context.Context, req *runtimeapi.StatusRequest) (*runtimeapi.StatusResponse, error) {
	return &runtimeapi.StatusResponse{
		Status: &runtimeapi.RuntimeStatus{
			Conditions: []*runtimeapi.RuntimeCondition{
				{
					Type:    "RuntimeReady",
					Status:  true,
					Reason:  "HvisorReady",
					Message: "Hvisor runtime is ready",
				},
				{
					Type:    "NetworkReady",
					Status:  true,
					Reason:  "NetworkReady",
					Message: "Network is ready",
				},
			},
		},
	}, nil
}

// store Pod Sandbox information
var podSandboxes = make(map[string]*runtimeapi.PodSandboxStatus)

const demoPodIP = "192.168.31.101" // demo pod IP
func (s *SimpleRuntimeService) RunPodSandbox(ctx context.Context, req *runtimeapi.RunPodSandboxRequest) (*runtimeapi.RunPodSandboxResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("RunPodSandbox called: name=%s, namespace=%s",
		req.Config.Metadata.Name,
		req.Config.Metadata.Namespace)

	podID := fmt.Sprintf("pod-%d", time.Now().UnixNano())
	log.Printf("Creating pod sandbox: %s", podID)

	// store Pod Sandbox status
	podSandboxes[podID] = &runtimeapi.PodSandboxStatus{
		Id:    podID,
		State: runtimeapi.PodSandboxState_SANDBOX_READY,
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      req.Config.Metadata.Name,
			Namespace: req.Config.Metadata.Namespace,
			Uid:       req.Config.Metadata.Uid,
			Attempt:   req.Config.Metadata.Attempt,
		},
		Network: &runtimeapi.PodSandboxNetworkStatus{
			Ip: demoPodIP,
		},
		CreatedAt: time.Now().UnixNano(),
	}

	log.Printf("Pod Sandbox created successfully: %s", podID)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	return &runtimeapi.RunPodSandboxResponse{PodSandboxId: podID}, nil
}

func (s *SimpleRuntimeService) StopPodSandbox(ctx context.Context, req *runtimeapi.StopPodSandboxRequest) (*runtimeapi.StopPodSandboxResponse, error) {
	log.Printf("StopPodSandbox called: pod=%s", req.PodSandboxId)
	return &runtimeapi.StopPodSandboxResponse{}, nil
}

func (s *SimpleRuntimeService) RemovePodSandbox(ctx context.Context, req *runtimeapi.RemovePodSandboxRequest) (*runtimeapi.RemovePodSandboxResponse, error) {
	log.Printf("RemovePodSandbox called: pod=%s", req.PodSandboxId)
	return &runtimeapi.RemovePodSandboxResponse{}, nil
}

func (s *SimpleRuntimeService) PodSandboxStatus(ctx context.Context, req *runtimeapi.PodSandboxStatusRequest) (*runtimeapi.PodSandboxStatusResponse, error) {
	if status, exists := podSandboxes[req.PodSandboxId]; exists {
		return &runtimeapi.PodSandboxStatusResponse{Status: status}, nil
	}

	// if not exists, return default status
	return &runtimeapi.PodSandboxStatusResponse{
		Status: &runtimeapi.PodSandboxStatus{
			Id:    req.PodSandboxId,
			State: runtimeapi.PodSandboxState_SANDBOX_READY,
			Metadata: &runtimeapi.PodSandboxMetadata{
				Name:      "unknown",
				Namespace: "default",
				Uid:       req.PodSandboxId,
				Attempt:   1,
			},
			Network: &runtimeapi.PodSandboxNetworkStatus{
				Ip: demoPodIP,
			},
		},
	}, nil
}

func (s *SimpleRuntimeService) ListPodSandbox(ctx context.Context, req *runtimeapi.ListPodSandboxRequest) (*runtimeapi.ListPodSandboxResponse, error) {
	// iterate through stored pod sandboxes, and filter as needed
	var sandboxes []*runtimeapi.PodSandbox
	for _, status := range podSandboxes {
		// filter condition: State
		if req.Filter != nil && req.Filter.State != nil {
			if status.State != req.Filter.State.State {
				continue
			}
		}
		sandboxes = append(sandboxes, &runtimeapi.PodSandbox{
			Id:          status.Id,
			Metadata:    status.Metadata,
			State:       status.State,
			CreatedAt:   status.CreatedAt,
			Labels:      status.Labels,
			Annotations: status.Annotations,
		})
	}
	return &runtimeapi.ListPodSandboxResponse{Items: sandboxes}, nil
}

func (s *SimpleRuntimeService) CreateContainer(ctx context.Context, req *runtimeapi.CreateContainerRequest) (*runtimeapi.CreateContainerResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("CreateContainer called")
	log.Printf("   Pod ID: %s", req.PodSandboxId)
	log.Printf("   Container Name: %s", req.Config.Metadata.Name)
	log.Printf("   Image: %s", req.Config.Image.Image)
	log.Printf("   current container count: %d", len(s.containers))

	containerID := fmt.Sprintf("vm-%d", time.Now().UnixNano())

	// generate log path to avoid kubelet ReopenContainerLog error
	logPath := fmt.Sprintf("/var/log/containers/%s.log", containerID)
	if err := ensureLogFile(logPath); err != nil {
		log.Printf("Failed to prepare log path %s: %v", logPath, err)
		return nil, err
	}

	// call hvisor to create VM
	if err := hvisorCreateVM(containerID, req.Config.Metadata.Name, req.Config.Image.Image); err != nil {
		log.Printf("Failed to create VM: %v", err)
		return nil, err
	}

	// store container information
	s.containers[containerID] = &ContainerInfo{
		ID:        containerID,
		Name:      req.Config.Metadata.Name,
		PodID:     req.PodSandboxId,
		Image:     req.Config.Image.Image,
		LogPath:   logPath,
		State:     runtimeapi.ContainerState_CONTAINER_CREATED,
		CreatedAt: time.Now().UnixNano(),
		Attempt:   int32(req.Config.Metadata.Attempt),
		Labels:    req.Config.Labels,
		Annotations: req.Config.Annotations,
		VMStarted: false, // initial state: VM not started
	}

	log.Printf("Created container: %s for pod: %s", containerID, req.PodSandboxId)
	log.Printf("new container count: %d", len(s.containers))
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.CreateContainerResponse{ContainerId: containerID}, nil
}

func (s *SimpleRuntimeService) StartContainer(ctx context.Context, req *runtimeapi.StartContainerRequest) (*runtimeapi.StartContainerResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("StartContainer called")
	log.Printf("   Container ID: %s", req.ContainerId)

	container, exists := s.containers[req.ContainerId]
	if !exists {
		log.Printf("Container not found: %s", req.ContainerId)
		return nil, fmt.Errorf("container not found: %s", req.ContainerId)
	}

	log.Printf("current state: %v, VMStarted: %v", container.State, container.VMStarted)
	log.Printf("image: %s", container.Image)

	// extract OS type
	osType := extractOSType(container.Image)
	if osType == "" {
		log.Printf("Failed to extract OS type from image name %s, delete container", container.Image)
		log.Printf("container count before deletion: %d", len(s.containers))
		// if start failed, delete container record
		delete(s.containers, req.ContainerId)
		log.Printf("container count after deletion: %d", len(s.containers))
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		return nil, fmt.Errorf("failed to extract OS type from image name %s", container.Image)
	}

	log.Printf("OS type: %s", osType)

	// get corresponding OS config to get ZoneID
	config, exists := supportedImages[osType]
	if !exists {
		log.Printf("Not supported OS type: %s", osType)
		delete(s.containers, req.ContainerId)
		return nil, fmt.Errorf("not supported OS type: %s", osType)
	}

	// call hvisor to start VM
	if err := hvisorStartVM(req.ContainerId, osType); err != nil {
		log.Printf("Failed to start VM: %v, delete container", err)
		log.Printf("container count before deletion: %d", len(s.containers))
		// if start failed, delete container record, avoid kubelet infinite retry
		delete(s.containers, req.ContainerId)
		log.Printf("container count after deletion: %d", len(s.containers))
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		return nil, err
	}

	// update container state
	container.State = runtimeapi.ContainerState_CONTAINER_RUNNING
	container.StartedAt = time.Now().UnixNano()
	container.VMStarted = true // mark VM as successfully started
	container.ZoneID = config.ZoneID // save ZoneID for exec

	log.Printf("Started container: %s", req.ContainerId)
	log.Printf("new state: %v, VMStarted: %v", container.State, container.VMStarted)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.StartContainerResponse{}, nil
}

func (s *SimpleRuntimeService) StopContainer(ctx context.Context, req *runtimeapi.StopContainerRequest) (*runtimeapi.StopContainerResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("StopContainer called")
	log.Printf("   Container ID: %s", req.ContainerId)
	log.Printf("   Timeout: %d seconds", req.Timeout)

	container, exists := s.containers[req.ContainerId]
	if !exists {
		log.Printf("Container %s not found in containers map", req.ContainerId)
		log.Printf("current container count: %d", len(s.containers))
		log.Printf("container list: ")
		for id, info := range s.containers {
			log.Printf("    - ID: %s, Name: %s, State: %v, VMStarted: %v", id, info.Name, info.State, info.VMStarted)
		}
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		return &runtimeapi.StopContainerResponse{}, nil
	}

	log.Printf("container information:")
	log.Printf("    name: %s", container.Name)
	log.Printf("    pod ID: %s", container.PodID)
	log.Printf("    image: %s", container.Image)
	log.Printf("    current state: %v", container.State)
	log.Printf("    VMStarted: %v", container.VMStarted)
	log.Printf("    CreatedAt: %d", container.CreatedAt)
	log.Printf("    StartedAt: %d", container.StartedAt)

	// if container is running, call hvisor to stop VM
	if container.State == runtimeapi.ContainerState_CONTAINER_RUNNING && container.VMStarted {
		// extract OS type
		osType := extractOSType(container.Image)
		if osType == "" {
			log.Printf("Failed to extract OS type from image name %s, skip VM stop", container.Image)
		} else {
			// call hvisor to stop VM
			if err := hvisorStopVM(req.ContainerId, osType); err != nil {
				log.Printf("Failed to stop VM: %v", err)
				// even if stop failed, update container state, avoid kubelet infinite retry
			}
		}
	}

	// update container state to stopped
	container.State = runtimeapi.ContainerState_CONTAINER_EXITED
	container.FinishedAt = time.Now().UnixNano()
	container.ExitCode = 0 // normal exit
	container.Reason = "Completed"
	container.VMStarted = false

	log.Printf("Container stopped")
	log.Printf("new state: %v", container.State)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.StopContainerResponse{}, nil
}

func (s *SimpleRuntimeService) RemoveContainer(ctx context.Context, req *runtimeapi.RemoveContainerRequest) (*runtimeapi.RemoveContainerResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("RemoveContainer called")
	log.Printf("   Container ID: %s", req.ContainerId)

	container, exists := s.containers[req.ContainerId]
	if !exists {
		log.Printf("Container %s not found in containers map", req.ContainerId)
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		return &runtimeapi.RemoveContainerResponse{}, nil
	}

	// if VM is still running, stop it first
	if container.State == runtimeapi.ContainerState_CONTAINER_RUNNING && container.VMStarted {
		log.Printf("VM is still running, stop it first")
		osType := extractOSType(container.Image)
		if osType != "" {
			_ = hvisorStopVM(req.ContainerId, osType)
		}
	}

	// call hvisor to delete VM (clean up resources)
	osType := extractOSType(container.Image)
	if osType != "" {
		if err := hvisorDeleteVM(req.ContainerId); err != nil {
			log.Printf("Failed to delete VM: %v", err)
		}
	}

	// delete container from containers map
	delete(s.containers, req.ContainerId)
	log.Printf("Container deleted")
	log.Printf("remaining container count: %d", len(s.containers))
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.RemoveContainerResponse{}, nil
}

func (s *SimpleRuntimeService) ListContainers(ctx context.Context, req *runtimeapi.ListContainersRequest) (*runtimeapi.ListContainersResponse, error) {
	// print detailed logs for debugging
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("ListContainers called")
	log.Printf("   Filter: %v", req.Filter)

	var containers []*runtimeapi.Container
	for _, info := range s.containers {
		// apply filters
		if req.Filter != nil {
			// filter by ID
			if req.Filter.Id != "" && info.ID != req.Filter.Id {
				continue
			}
			// filter by PodSandboxId
			if req.Filter.PodSandboxId != "" && info.PodID != req.Filter.PodSandboxId {
				continue
			}
			// filter by state
			if req.Filter.State != nil && info.State != req.Filter.State.State {
				log.Printf("skip container %s (state %v != filter %v)", info.ID, info.State, req.Filter.State.State)
				continue
			}
		}

		container := &runtimeapi.Container{
			Id: info.ID,
			Metadata: &runtimeapi.ContainerMetadata{
				Name:    info.Name,
				Attempt: uint32(info.Attempt),
			},
			Image:       &runtimeapi.ImageSpec{Image: info.Image},
			ImageRef:    info.Image,
			State:       info.State,
			CreatedAt:   info.CreatedAt,
			Labels:      info.Labels,
			Annotations: info.Annotations,
			PodSandboxId: info.PodID,
		}
		containers = append(containers, container)
	}

	log.Printf("return %d containers:", len(containers))
	for i, c := range containers {
		log.Printf("     [%d] ID: %s, Name: %s, State: %v", i+1, c.Id, c.Metadata.Name, c.State)
	}
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.ListContainersResponse{Containers: containers}, nil
}

func (s *SimpleRuntimeService) ContainerStatus(ctx context.Context, req *runtimeapi.ContainerStatusRequest) (*runtimeapi.ContainerStatusResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("ContainerStatus called")
	log.Printf("   Container ID: %s", req.ContainerId)

	container, exists := s.containers[req.ContainerId]
	if !exists {
		log.Printf("Container not found: %s", req.ContainerId)
		return nil, fmt.Errorf("container not found: %s", req.ContainerId)
	}

	status := &runtimeapi.ContainerStatus{
		Id: container.ID,
		Metadata: &runtimeapi.ContainerMetadata{
			Name:    container.Name,
			Attempt: uint32(container.Attempt),
		},
		Image: &runtimeapi.ImageSpec{
			Image: container.Image,
		},
		ImageRef:    container.Image,
		State:       container.State,
		LogPath:     container.LogPath,
		CreatedAt:   container.CreatedAt,
		StartedAt:   container.StartedAt,
		Labels:      container.Labels,
		Annotations: container.Annotations,
	}

	log.Printf("return status:")
	log.Printf("    ID: %s", status.Id)
	log.Printf("    Name: %s", status.Metadata.Name)
	log.Printf("    state: %v", status.State)
	log.Printf("    CreatedAt: %d", status.CreatedAt)
	log.Printf("    StartedAt: %d", status.StartedAt)
	log.Printf("    LogPath: %s", status.LogPath)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.ContainerStatusResponse{Status: status}, nil
}

// other CRI interfaces simple implementation
func (s *SimpleRuntimeService) Attach(ctx context.Context, req *runtimeapi.AttachRequest) (*runtimeapi.AttachResponse, error) {
	log.Println("Attach called")
	return &runtimeapi.AttachResponse{Url: "http://localhost/attach"}, nil
}

func (s *SimpleRuntimeService) Exec(ctx context.Context, req *runtimeapi.ExecRequest) (*runtimeapi.ExecResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("Exec called (streaming)")
	log.Printf("   Container ID: %s", req.ContainerId)
	log.Printf("   Command: %v", req.Cmd)
	log.Printf("   Stdin: %v, Stdout: %v, Stderr: %v", req.Stdin, req.Stdout, req.Stderr)
	log.Printf("   TTY: %v", req.Tty)

	// check if container exists and is running
	container, exists := s.containers[req.ContainerId]
	if !exists {
		log.Printf("Container not found: %s", req.ContainerId)
		return nil, fmt.Errorf("container not found: %s", req.ContainerId)
	}

	if container.State != runtimeapi.ContainerState_CONTAINER_RUNNING {
		log.Printf("Container is not running: state=%v", container.State)
		return nil, fmt.Errorf("container is not running: state=%v", container.State)
	}

	// build URL (contains container-id and command parameters)
	// kubelet will pass command through query parameters
	cmdParam := strings.Join(req.Cmd, " ")
	url := fmt.Sprintf("http://127.0.0.1:10251/hvisor/exec/%s?cmd=%s", req.ContainerId, cmdParam)
	if req.Tty {
		url += "&tty=1"
	}

	log.Printf("Exec: returning URL: %s", url)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.ExecResponse{Url: url}, nil
}

func (s *SimpleRuntimeService) ExecSync(ctx context.Context, req *runtimeapi.ExecSyncRequest) (*runtimeapi.ExecSyncResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("ExecSync called")
	log.Printf("   Container ID: %s", req.ContainerId)
	log.Printf("   Command: %v", req.Cmd)
	log.Printf("   Timeout: %d seconds", req.Timeout)

	// check if container exists
	container, exists := s.containers[req.ContainerId]
	if !exists {
		log.Printf("Container not found: %s", req.ContainerId)
		return nil, fmt.Errorf("container not found: %s", req.ContainerId)
	}

	// check container state (must be RUNNING to execute command)
	if container.State != runtimeapi.ContainerState_CONTAINER_RUNNING {
		log.Printf("Container is not running: state=%v", container.State)
		return nil, fmt.Errorf("container is not running: state=%v", container.State)
	}

	// minimum demo: call external binary hvisor-exec-helper
	execResp, err := execViaHelper(ctx, req.Cmd, int(req.Timeout))
	if err != nil {
		log.Printf("ExecSync helper failed: %v", err)
		return &runtimeapi.ExecSyncResponse{
			Stdout:   []byte(""),
			Stderr:   []byte(err.Error()),
			ExitCode: -1,
		}, nil
	}

	log.Printf("ExecSync: Command executed successfully (exit_code=%d)", execResp.ExitCode)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	return &runtimeapi.ExecSyncResponse{
		Stdout:   []byte(execResp.Stdout),
		Stderr:   []byte(execResp.Stderr),
		ExitCode: int32(execResp.ExitCode),
	}, nil
}

type helperRequest struct {
	Cmd        []string `json:"cmd"`
	TimeoutSec int      `json:"timeout_sec"`
}

type helperResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func execViaHelper(parent context.Context, cmd []string, timeoutSec int) (*helperResponse, error) {
	helperPath := os.Getenv("HVISOR_EXEC_HELPER_PATH")
	if helperPath == "" {
		helperPath = "/root/hvisor-exec-helper"
	}

	if timeoutSec <= 0 {
		timeoutSec = 10
	}

	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutSec+5)*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, helperPath)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	req := helperRequest{
		Cmd:        cmd,
		TimeoutSec: timeoutSec,
	}
	reqBytes, _ := json.Marshal(req)
	c.Stdin = bytes.NewReader(append(reqBytes, '\n'))

	if err := c.Run(); err != nil {
		// helper stderr as diagnostic information
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("exec-helper failed: %s", msg)
	}

	var resp helperResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("exec-helper invalid json: %w (raw=%q)", err, stdout.String())
	}
	if resp.Error != "" {
		return &resp, fmt.Errorf(resp.Error)
	}
	return &resp, nil
}

// executeViaProxy execute command via exec-proxy
func (s *SimpleRuntimeService) executeViaProxy(zoneID string, command []string, timeout int) (*ExecProxyResponse, error) {
	// connect to exec-proxy
	socketPath := "/var/run/hvisor-exec-proxy.sock"
	if envPath := os.Getenv("EXEC_PROXY_SOCKET"); envPath != "" {
		socketPath = envPath
	}

	conn, err := net.DialTimeout("unix", socketPath, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to exec-proxy: %w", err)
	}
	defer conn.Close()

	// set timeout
	if timeout <= 0 {
		timeout = 10
	}
	conn.SetDeadline(time.Now().Add(time.Duration(timeout+5) * time.Second))

	// send request
	req := ExecProxyRequest{
		ZoneID:  zoneID,
		Command: command,
		Timeout: timeout,
	}

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// receive response
	var resp ExecProxyResponse
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to receive response: %w", err)
	}

	if resp.Error != "" {
		return &resp, fmt.Errorf("exec-proxy error: %s", resp.Error)
	}

	return &resp, nil
}

// ExecProxyRequest exec-proxy request
type ExecProxyRequest struct {
	ZoneID  string   `json:"zone_id"`
	Command []string `json:"command"`
	Timeout int      `json:"timeout"`
}

// ExecProxyResponse exec-proxy response
type ExecProxyResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func (s *SimpleRuntimeService) PortForward(ctx context.Context, req *runtimeapi.PortForwardRequest) (*runtimeapi.PortForwardResponse, error) {
	log.Println("PortForward called")
	return &runtimeapi.PortForwardResponse{Url: "http://localhost/portforward"}, nil
}

func (s *SimpleRuntimeService) UpdateRuntimeConfig(ctx context.Context, req *runtimeapi.UpdateRuntimeConfigRequest) (*runtimeapi.UpdateRuntimeConfigResponse, error) {
	log.Println("UpdateRuntimeConfig called")
	return &runtimeapi.UpdateRuntimeConfigResponse{}, nil
}

func (s *SimpleRuntimeService) UpdateContainerResources(ctx context.Context, req *runtimeapi.UpdateContainerResourcesRequest) (*runtimeapi.UpdateContainerResourcesResponse, error) {
	log.Println("UpdateContainerResources called")
	return &runtimeapi.UpdateContainerResourcesResponse{}, nil
}

func (s *SimpleRuntimeService) ReopenContainerLog(ctx context.Context, req *runtimeapi.ReopenContainerLogRequest) (*runtimeapi.ReopenContainerLogResponse, error) {
	log.Println("ReopenContainerLog called")
	if req.ContainerId != "" {
		if c, ok := s.containers[req.ContainerId]; ok && c.LogPath != "" {
			_ = ensureLogFile(c.LogPath)
		}
	}
	return &runtimeapi.ReopenContainerLogResponse{}, nil
}

func (s *SimpleRuntimeService) ContainerStats(ctx context.Context, req *runtimeapi.ContainerStatsRequest) (*runtimeapi.ContainerStatsResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("ContainerStats called")
	log.Printf("   Container ID: %s", req.ContainerId)
	log.Printf("    current container count: %d", len(s.containers))
	for id := range s.containers {
		log.Printf("     container ID: %s", id)
	}

	// try exact match first
	container, exists := s.containers[req.ContainerId]
	if !exists {
		// try prefix match (support short ID)
		log.Printf("    exact match failed, try prefix match...")
		for id, c := range s.containers {
			if strings.HasPrefix(id, req.ContainerId) {
				log.Printf("    prefix match success: request ID=%s, actual ID=%s", req.ContainerId, id)
				container = c
				exists = true
				break
			}
		}
	}

	if !exists {
		log.Printf("Container not found: %s", req.ContainerId)
		return nil, fmt.Errorf("container not found: %s", req.ContainerId)
	}

	// return basic statistics structure (placeholder data)
	now := time.Now().UnixNano()
	stats := &runtimeapi.ContainerStats{
		Attributes: &runtimeapi.ContainerAttributes{
			Id: container.ID,
			Metadata: &runtimeapi.ContainerMetadata{
				Name:    container.Name,
				Attempt: uint32(container.Attempt),
			},
			Labels:      container.Labels,
			Annotations: container.Annotations,
		},
		Cpu: &runtimeapi.CpuUsage{
			Timestamp:            now,
			UsageCoreNanoSeconds: &runtimeapi.UInt64Value{Value: 0},
			UsageNanoCores:       &runtimeapi.UInt64Value{Value: 0},
		},
		Memory: &runtimeapi.MemoryUsage{
			Timestamp:       now,
			WorkingSetBytes: &runtimeapi.UInt64Value{Value: 0},
			AvailableBytes:  &runtimeapi.UInt64Value{Value: 0},
			UsageBytes:      &runtimeapi.UInt64Value{Value: 0},
			RssBytes:        &runtimeapi.UInt64Value{Value: 0},
			PageFaults:       &runtimeapi.UInt64Value{Value: 0},
			MajorPageFaults: &runtimeapi.UInt64Value{Value: 0},
		},
		WritableLayer: &runtimeapi.FilesystemUsage{
			Timestamp:  now,
			UsedBytes:  &runtimeapi.UInt64Value{Value: 0},
			InodesUsed: &runtimeapi.UInt64Value{Value: 0},
		},
	}

	log.Printf("ContainerStats return success")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.ContainerStatsResponse{Stats: stats}, nil
}

func (s *SimpleRuntimeService) ListContainerStats(ctx context.Context, req *runtimeapi.ListContainerStatsRequest) (*runtimeapi.ListContainerStatsResponse, error) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("ListContainerStats called")
	if req.Filter != nil {
		log.Printf("   Filter ID: %s", req.Filter.Id)
		log.Printf("   Filter PodSandboxId: %s", req.Filter.PodSandboxId)
	}

	log.Printf("    current container count: %d", len(s.containers))
	for id, container := range s.containers {
		log.Printf("     container: ID=%s, Name=%s, State=%v", id, container.Name, container.State)
	}

	var statsList []*runtimeapi.ContainerStats
	now := time.Now().UnixNano()

	// iterate all containers, return statistics for all containers (including non-running)
	for _, container := range s.containers {
		// if filter is specified, filter the containers
		if req.Filter != nil {
			if req.Filter.Id != "" && container.ID != req.Filter.Id {
				log.Printf("    skip container %s (ID mismatch: %s != %s)", container.ID, container.ID, req.Filter.Id)
				continue
			}
			if req.Filter.PodSandboxId != "" && container.PodID != req.Filter.PodSandboxId {
				log.Printf("    skip container %s (PodSandboxId mismatch: %s != %s)", container.ID, container.PodID, req.Filter.PodSandboxId)
				continue
			}
		}

		log.Printf("    process container: ID=%s, Name=%s, State=%v", container.ID, container.Name, container.State)

		// return basic statistics structure 
		// note: crictl may filter out all zero data
		stats := &runtimeapi.ContainerStats{
			Attributes: &runtimeapi.ContainerAttributes{
				Id: container.ID,
				Metadata: &runtimeapi.ContainerMetadata{
					Name:    container.Name,
					Attempt: uint32(container.Attempt),
				},
				Labels:      container.Labels,
				Annotations: container.Annotations,
			},
			Cpu: &runtimeapi.CpuUsage{
				Timestamp:            now,
				UsageCoreNanoSeconds: &runtimeapi.UInt64Value{Value: 1000000}, // 1ms = 1,000,000 纳秒
				UsageNanoCores:       &runtimeapi.UInt64Value{Value: 1000000}, // 1ms/s = 1,000,000 纳秒/秒
			},
			Memory: &runtimeapi.MemoryUsage{
				Timestamp:       now,
				WorkingSetBytes: &runtimeapi.UInt64Value{Value: 1024},      // 1KB
				AvailableBytes:  &runtimeapi.UInt64Value{Value: 1048576},  // 1MB
				UsageBytes:      &runtimeapi.UInt64Value{Value: 1024},     // 1KB
				RssBytes:        &runtimeapi.UInt64Value{Value: 1024},     // 1KB
				PageFaults:       &runtimeapi.UInt64Value{Value: 0},
				MajorPageFaults: &runtimeapi.UInt64Value{Value: 0},
			},
			WritableLayer: &runtimeapi.FilesystemUsage{
				Timestamp:  now,
				UsedBytes:  &runtimeapi.UInt64Value{Value: 1024},    // 1KB
				InodesUsed: &runtimeapi.UInt64Value{Value: 1},       // 1 inode
			},
		}
		statsList = append(statsList, stats)
		log.Printf("    add container %s statistics (CPU=1ms, Memory=1KB)", container.ID)
	}

	if len(statsList) == 0 {
		log.Printf("no containers found, force return a test data")
		now := time.Now().UnixNano()
		testStats := &runtimeapi.ContainerStats{
			Attributes: &runtimeapi.ContainerAttributes{
				Id: "test-container-0",
				Metadata: &runtimeapi.ContainerMetadata{
					Name:    "test-container",
					Attempt: 0,
				},
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
			},
			Cpu: &runtimeapi.CpuUsage{
				Timestamp:            now,
				UsageCoreNanoSeconds: &runtimeapi.UInt64Value{Value: 1000000}, // 1ms
				UsageNanoCores:       &runtimeapi.UInt64Value{Value: 1000000}, // 1ms/s
			},
			Memory: &runtimeapi.MemoryUsage{
				Timestamp:       now,
				WorkingSetBytes: &runtimeapi.UInt64Value{Value: 1024},      // 1KB
				AvailableBytes:  &runtimeapi.UInt64Value{Value: 1048576},  // 1MB
				UsageBytes:      &runtimeapi.UInt64Value{Value: 1024},     // 1KB
				RssBytes:        &runtimeapi.UInt64Value{Value: 1024},     // 1KB
				PageFaults:       &runtimeapi.UInt64Value{Value: 0},
				MajorPageFaults: &runtimeapi.UInt64Value{Value: 0},
			},
			WritableLayer: &runtimeapi.FilesystemUsage{
				Timestamp:  now,
				UsedBytes:  &runtimeapi.UInt64Value{Value: 1024},    // 1KB
				InodesUsed: &runtimeapi.UInt64Value{Value: 1},       // 1 inode
			},
		}
		statsList = append(statsList, testStats)
		log.Printf("    add test container test-container-0")
	}

	log.Printf("ListContainerStats return %d containers statistics", len(statsList))
	for i, stats := range statsList {
		cpuVal := uint64(0)
		memVal := uint64(0)
		if stats.Cpu != nil && stats.Cpu.UsageNanoCores != nil {
			cpuVal = stats.Cpu.UsageNanoCores.Value
		}
		if stats.Memory != nil && stats.Memory.WorkingSetBytes != nil {
			memVal = stats.Memory.WorkingSetBytes.Value
		}
		log.Printf("     [%d] ID: %s, Name: %s, CPU=%d ns, Memory=%d bytes", i+1, stats.Attributes.Id, stats.Attributes.Metadata.Name, cpuVal, memVal)
	}
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return &runtimeapi.ListContainerStatsResponse{Stats: statsList}, nil
}

func (s *SimpleRuntimeService) PodSandboxStats(ctx context.Context, req *runtimeapi.PodSandboxStatsRequest) (*runtimeapi.PodSandboxStatsResponse, error) {
	log.Println("PodSandboxStats called")
	return &runtimeapi.PodSandboxStatsResponse{}, nil
}

func (s *SimpleRuntimeService) ListPodSandboxStats(ctx context.Context, req *runtimeapi.ListPodSandboxStatsRequest) (*runtimeapi.ListPodSandboxStatsResponse, error) {
	log.Println("ListPodSandboxStats called")
	return &runtimeapi.ListPodSandboxStatsResponse{}, nil
}

// ============================================================================
// Image Service
// ============================================================================

type SimpleImageService struct{}

func (s *SimpleImageService) ListImages(ctx context.Context, req *runtimeapi.ListImagesRequest) (*runtimeapi.ListImagesResponse, error) {
	// don't print logs, this is health check called frequently by kubelet
	return &runtimeapi.ListImagesResponse{}, nil
}

func (s *SimpleImageService) ImageStatus(ctx context.Context, req *runtimeapi.ImageStatusRequest) (*runtimeapi.ImageStatusResponse, error) {
	// get image reference
	imageRef := req.Image.Image
	if imageRef == "" {
		imageRef = "linux"
	}

	// extract image type
	osType := extractOSType(imageRef)
	if osType == "" {
		log.Printf("ImageStatus: image=%s unsupported image type", imageRef)
		return nil, fmt.Errorf("unsupported image type: %s", imageRef)
	}

	log.Printf("ImageStatus: image=%s (type: %s, return exists)", imageRef, osType)

	// return image exists status, let kubelet skip image pull
	return &runtimeapi.ImageStatusResponse{
		Image: &runtimeapi.Image{
			Id:          imageRef,
			RepoDigests: []string{imageRef + "@sha256:local"},
			RepoTags:    []string{imageRef},
			Size_:       1024 * 1024 * 100,
		},
	}, nil
}

func (s *SimpleImageService) PullImage(ctx context.Context, req *runtimeapi.PullImageRequest) (*runtimeapi.PullImageResponse, error) {
	// get image reference
	imageRef := req.Image.Image
	if imageRef == "" {
		imageRef = "linux"
	}

	// extract image type and check if it is supported
	osType := extractOSType(imageRef)
	if osType == "" {
		log.Printf("PullImage called: image=%s (unsupported image type)", imageRef)
		return nil, fmt.Errorf("unsupported image type: %s", imageRef)
	}

	log.Printf("PullImage called: image=%s (type: %s, demo mode: skip image pull)", imageRef, osType)

	return &runtimeapi.PullImageResponse{ImageRef: imageRef}, nil
}

func (s *SimpleImageService) RemoveImage(ctx context.Context, req *runtimeapi.RemoveImageRequest) (*runtimeapi.RemoveImageResponse, error) {
	return &runtimeapi.RemoveImageResponse{}, nil
}

func (s *SimpleImageService) ImageFsInfo(ctx context.Context, req *runtimeapi.ImageFsInfoRequest) (*runtimeapi.ImageFsInfoResponse, error) {
	return &runtimeapi.ImageFsInfoResponse{}, nil
}

// ensure log file exists and 
func ensureLogFile(path string) error {
	if path == "" {
		return fmt.Errorf("log path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create log dir %s: %w", dir, err)
	}
	// if file does not exist, create an empty file
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			return fmt.Errorf("failed to create log file %s: %w", path, err)
		}
	}
	return nil
}

// ============================================================================
// Main
// ============================================================================

func main() {
	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Println("hvisor-cri - Kubernetes CRI for hvisor")
	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Println("version: 0.1.0")
	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Println("")

	// clean up old socket file
	socketPath := "/var/run/hvisor-cri.sock"
	os.Remove(socketPath)

	// create Unix socket listener
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// create gRPC server
	grpcServer := grpc.NewServer()

	// register services
	runtimeService := &SimpleRuntimeService{
		containers: make(map[string]*ContainerInfo),
	}
	imageService := &SimpleImageService{}

	runtimeapi.RegisterRuntimeServiceServer(grpcServer, runtimeService)
	runtimeapi.RegisterImageServiceServer(grpcServer, imageService)

	log.Printf("CRI listening on: %s", socketPath)
	log.Println("waiting for kubelet connection...")
	log.Println("")

	// start HTTP server (for Exec/Attach streaming interface)
	go startExecServer(runtimeService)

	// start gRPC server
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}

// startExecServer start HTTP server to handle Exec/Attach streaming requests
func startExecServer(runtimeService *SimpleRuntimeService) {
	mux := http.NewServeMux()
	mux.HandleFunc("/hvisor/exec/", runtimeService.handleExecStream)

	server := &http.Server{
		Addr:    "127.0.0.1:10251",
		Handler: mux,
	}

	log.Printf("Exec server listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Exec server failed: %v", err)
	}
}

// handleExecStream handles Exec streaming requests from kubelet (kubectl exec).
// It supports SPDY upgrade and multiplexed streams for stdin/stdout/stderr/error.
func (s *SimpleRuntimeService) handleExecStream(w http.ResponseWriter, r *http.Request) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("handleExecStream called")
	log.Printf("Method: %s", r.Method)
	log.Printf("Path: %s", r.URL.Path)
	log.Printf("Query: %s", r.URL.RawQuery)
	log.Printf("Headers: %v", r.Header)
	log.Printf("RemoteAddr: %s", r.RemoteAddr)

	// extract container-id from URL
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/hvisor/exec/"), "/")
	if len(pathParts) == 0 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	containerID := pathParts[0]

	// check if container exists and is running
	container, exists := s.containers[containerID]
	if !exists {
		log.Printf("container not found: %s", containerID)
		http.Error(w, "container not found", http.StatusNotFound)
		return
	}

	if container.State != runtimeapi.ContainerState_CONTAINER_RUNNING {
		log.Printf("container is not running: state=%v", container.State)
		http.Error(w, "container is not running", http.StatusBadRequest)
		return
	}

	// extract command from query parameters
	q := r.URL.Query()
	cmdStr := q.Get("cmd")
	if cmdStr == "" && len(pathParts) > 1 {
		cmdStr = strings.Join(pathParts[1:], " ")
	}
	if cmdStr == "" {
		http.Error(w, "missing cmd parameter", http.StatusBadRequest)
		return
	}

	// parse command (kubelet passes a space-separated string)
	cmd := strings.Fields(cmdStr)
	if len(cmd) == 0 {
		http.Error(w, "empty cmd", http.StatusBadRequest)
		return
	}

	log.Printf("Container: %s", containerID)
	log.Printf("Command: %v", cmd)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// SPDY upgrade branch for kubectl exec.
	if httpstream.IsUpgradeRequest(r) {
		log.Printf("Upgrade request detected, starting SPDY exec")

		// 1. negotiate stream protocol version
		negotiatedProtocol, err := httpstream.Handshake(r, w, apiremotecommand.SupportedStreamingProtocols)
		if err != nil {
			log.Printf("SPDY handshake failed: %v", err)
			return
		}
		log.Printf("SPDY negotiated protocol: %s", negotiatedProtocol)

		// 2. collect streams created by kubelet (stdin/stdout/stderr/error/resize)
		type streamSet struct {
			stdin  httpstream.Stream
			stdout httpstream.Stream
			stderr httpstream.Stream
			err    httpstream.Stream
			resize httpstream.Stream
		}
		var streams streamSet
		readyCh := make(chan struct{})

		// kubelet decides which streams to open based on query parameters.
		expected := 1 // error stream is always present
		if q.Get("stdin") == "1" || q.Get("stdin") == "true" {
			expected++
		}
		if q.Get("stdout") == "1" || q.Get("stdout") == "true" || q.Get("stdout") == "" {
			expected++
		}
		if (q.Get("stderr") == "1" || q.Get("stderr") == "true" || q.Get("stderr") == "") &&
			!(q.Get("tty") == "1" || q.Get("tty") == "true") {
			expected++
		}
		if q.Get("resize") == "1" || q.Get("resize") == "true" {
			expected++
		}

		got := 0
		newStreamHandler := func(stream httpstream.Stream, replySent <-chan struct{}) error {
			st := stream.Headers().Get("streamType")
			log.Printf("SPDY new stream: id=%d streamType=%q headers=%v", stream.Identifier(), st, stream.Headers())
			switch st {
			case "stdin":
				streams.stdin = stream
			case "stdout":
				streams.stdout = stream
			case "stderr":
				streams.stderr = stream
			case "error":
				streams.err = stream
			case "resize":
				streams.resize = stream
			}
			got++
			if got >= expected {
				select {
				case <-readyCh:
				default:
					close(readyCh)
				}
			}
			return nil
		}

		// 3. SPDY upgrade (returns 101 Switching Protocols)
		upgrader := httpstreamspdy.NewResponseUpgrader()
		conn := upgrader.UpgradeResponse(w, r, newStreamHandler)
		if conn == nil {
			log.Printf("SPDY upgrade failed: conn is nil")
			return
		}
		defer conn.Close()

		select {
		case <-readyCh:
		case <-time.After(10 * time.Second):
			log.Printf("timeout waiting for SPDY streams (got=%d expected=%d)", got, expected)
			return
		case <-r.Context().Done():
			return
		}

		// 4. execute command via helper
		timeoutSec := 30
		if t := q.Get("timeout"); t != "" {
			// keep default if parse fails
			if n, err := fmt.Sscanf(t, "%d", &timeoutSec); n == 1 && err == nil && timeoutSec <= 0 {
				timeoutSec = 30
			}
		}
		resp, execErr := execViaHelper(r.Context(), cmd, timeoutSec)

		// 5. write stdout/stderr (append newline if missing)
		if streams.stdout != nil && resp != nil && resp.Stdout != "" {
			out := resp.Stdout
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			_, _ = io.WriteString(streams.stdout, out)
		}
		if streams.stderr != nil && resp != nil && resp.Stderr != "" {
			errOut := resp.Stderr
			if !strings.HasSuffix(errOut, "\n") {
				errOut += "\n"
			}
			_, _ = io.WriteString(streams.stderr, errOut)
		}

		// 6. write error stream with metav1.Status and exit code
		if streams.err != nil {
			exitCode := 0
			errMsg := ""
			if resp != nil {
				exitCode = resp.ExitCode
				errMsg = resp.Stderr
			} else if execErr != nil {
				exitCode = 1
				errMsg = execErr.Error()
			}

			enc := json.NewEncoder(streams.err)
			if exitCode == 0 {
				_ = enc.Encode(&metav1.Status{Status: metav1.StatusSuccess})
			} else {
				_ = enc.Encode(&metav1.Status{
					Status:  metav1.StatusFailure,
					Reason:  apiremotecommand.NonZeroExitCodeReason,
					Message: errMsg,
					Details: &metav1.StatusDetails{
						Causes: []metav1.StatusCause{
							{
								Type:    apiremotecommand.ExitCodeCauseType,
								Message: fmt.Sprintf("%d", exitCode),
							},
						},
					},
				})
			}
		}

		log.Printf("SPDY exec finished")
		return
	}

	// Non-upgrade HTTP request (e.g. curl for debugging): run once and return plain text.
	log.Printf("Non-upgrade HTTP request, running command once")
	resp, err := execViaHelper(r.Context(), cmd, 30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if resp.Stdout != "" {
		out := resp.Stdout
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		_, _ = io.WriteString(w, out)
	}
	if resp.Stderr != "" {
		errOut := resp.Stderr
		if !strings.HasSuffix(errOut, "\n") {
			errOut += "\n"
		}
		_, _ = io.WriteString(w, errOut)
	}
	log.Printf("Non-upgrade exec finished")
}

