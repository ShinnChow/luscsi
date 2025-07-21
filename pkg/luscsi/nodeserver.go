package luscsi

import (
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	klog "k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/volume"
	mount "k8s.io/mount-utils"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type NodeServer struct {
	Driver
	csi.NodeServer
}

func NewNodeServer(driver Driver) *NodeServer {
	return &NodeServer{
		Driver: driver,
	}
}

func (d *NodeServer) getLusVolumeFromRequest(req *csi.NodePublishVolumeRequest) (*lustreVolume, error) {
	lusVol := &lustreVolume{}
	volCap := req.GetVolumeCapability()
	if volCap == nil || volCap.GetMount() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}
	lusVol.volCap = volCap

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}
	lusVol.targetPath = targetPath

	// NOTE: volumeID is optional, for static volume, this filed will not be set.
	volumeID, _ := req.GetVolumeContext()[StorageVolumeID]
	lusVol.volID = volumeID

	mgsAddress, ok := req.GetVolumeContext()[StorageParamMgsAddress]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "mgsAddress is not provided")
	}
	lusVol.mgsAddress = mgsAddress

	fsName, ok := req.GetVolumeContext()[StorageParamFsName]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "fsName is not provided")
	}
	lusVol.fsName = fsName

	sharePath, ok := req.GetVolumeContext()[StorageParamSharePath]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "sharePath is not provided")
	}
	lusVol.sharePath = sharePath

	// NOTE: subDir is optional, if not provided, it will use the sharePath to mount in the container.
	// This is useful when data already existed in Lustre and will be shared between pods.
	subDir, _ := req.GetVolumeContext()[StorageParamSubdir]
	lusVol.subDir = subDir

	return lusVol, nil
}

func (d *NodeServer) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	lusVol, err := d.getLusVolumeFromRequest(req)
	if err != nil {
		klog.V(2).ErrorS(err, "failed to get volume info")
		return nil, status.Errorf(codes.InvalidArgument, "failed to get volume info: %s", err.Error())
	}

	mountOptions := lusVol.volCap.GetMount().GetMountFlags()
	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	// todo(ming): make this configurable from the storageclass parameters
	mountPermissions := d.MountPermissions
	source := filepath.Join(lusVol.mgsAddress+string(filepath.ListSeparator), lusVol.fsName, lusVol.sharePath, lusVol.subDir)
	notMnt, err := d.mounter.IsLikelyNotMountPoint(lusVol.targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(lusVol.targetPath, os.FileMode(mountPermissions)); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			notMnt = true
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	if !notMnt {
		klog.V(2).Infof("NodePublishVolume: volumeID(%v) already mounted on targetPath(%s) source(%s) mountflags(%v)", lusVol.volID, lusVol.targetPath, source, mountOptions)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	klog.V(2).Infof("NodePublishVolume: volumeID(%v) source(%s) targetPath(%s) mountflags(%v)", lusVol.volID, source, lusVol.targetPath, mountOptions)
	execFunc := func() error {
		return d.mounter.Mount(source, lusVol.targetPath, "lustre", mountOptions)
	}
	timeoutFunc := func() error { return fmt.Errorf("time out") }
	if err := WaitUntilTimeout(90*time.Second, execFunc, timeoutFunc); err != nil {
		if os.IsPermission(err) {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		if strings.Contains(err.Error(), "invalid argument") {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	if mountPermissions > 0 {
		if err := chmodIfPermissionMismatch(lusVol.targetPath, os.FileMode(mountPermissions)); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else {
		klog.V(2).Infof("skip chmod on targetPath(%s) since mountPermissions is set as 0", lusVol.targetPath)
	}

	klog.V(2).Infof("volume(%s) mount %s on %s succeeded", lusVol.volID, source, lusVol.targetPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

func chmodIfPermissionMismatch(targetPath string, mode os.FileMode) error {
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	perm := info.Mode() & os.ModePerm
	if perm != mode {
		klog.V(2).Infof("chmod targetPath(%s, mode:0%o) with permissions(0%o)", targetPath, info.Mode(), mode)
		if err := os.Chmod(targetPath, mode); err != nil {
			return err
		}
	} else {
		klog.V(2).Infof("skip chmod on targetPath(%s) since mode is already 0%o)", targetPath, info.Mode())
	}
	return nil
}

func (d *NodeServer) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	volID := req.GetVolumeId()
	if len(volID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	volumeSlice := strings.Split(volID, "#")
	if len(volumeSlice) != 4 {
		return nil, status.Error(codes.InvalidArgument, "invalid volumeID")
	}

	lusVol := &lustreVolume{}
	lusVol.volID = volumeSlice[3]
	lusVol.targetPath = targetPath

	klog.V(2).Infof("NodeUnpublishVolume: unmounting volume %s on %s", lusVol.volID, lusVol.targetPath)
	var err error
	forceUnmounter, ok := d.mounter.(mount.MounterForceUnmounter)
	if ok {
		klog.V(2).Infof("force unmount %s on %s", lusVol.volID, lusVol.targetPath)
		err = mount.CleanupMountWithForce(targetPath, forceUnmounter, true, 30*time.Second)
	} else {
		err = mount.CleanupMountPoint(targetPath, d.mounter, true)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount target %q: %v", targetPath, err)
	}

	klog.V(2).Infof("NodeUnpublishVolume: unmount volume %s on %s successfully", lusVol.volID, lusVol.targetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *NodeServer) NodeStageVolume(_ context.Context, _ *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeStageVolume is not implemented")
}

// Staging and Unstaging is not able to be supported with how Lustre is mounted
//
// See NodeStageVolume for more details
func (d *NodeServer) NodeUnstageVolume(_ context.Context, _ *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeUnstageVolume is not implemented")
}

func (d *NodeServer) NodeGetCapabilities(
	_ context.Context, _ *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: d.nscap,
	}, nil
}

// NodeGetInfo return info of the node on which this plugin is running
func (d *NodeServer) NodeGetInfo(
	_ context.Context,
	_ *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: d.NodeID,
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				DefaultTopologyKey: DefaultDriverName,
			}},
	}, nil
}

func (d *NodeServer) NodeGetVolumeStats(_ context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	if len(req.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats Volume ID was empty")
	}
	if len(req.VolumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats Volume path was empty")
	}

	if _, err := os.Lstat(req.VolumePath); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "path %s does not exist", req.VolumePath)
		}
		return nil, status.Errorf(codes.Internal, "failed to stat file %s: %v", req.VolumePath, err)
	}

	volumeMetrics, err := volume.NewMetricsStatFS(req.VolumePath).GetMetrics()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get metrics: %v", err)
	}

	available, ok := volumeMetrics.Available.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume available size(%v)", volumeMetrics.Available)
	}
	capacity, ok := volumeMetrics.Capacity.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume capacity size(%v)", volumeMetrics.Capacity)
	}
	used, ok := volumeMetrics.Used.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume used size(%v)", volumeMetrics.Used)
	}

	inodesFree, ok := volumeMetrics.InodesFree.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes free(%v)", volumeMetrics.InodesFree)
	}
	inodes, ok := volumeMetrics.Inodes.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes(%v)", volumeMetrics.Inodes)
	}
	inodesUsed, ok := volumeMetrics.InodesUsed.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes used(%v)", volumeMetrics.InodesUsed)
	}

	resp := csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: available,
				Total:     capacity,
				Used:      used,
			},
			{
				Unit:      csi.VolumeUsage_INODES,
				Available: inodesFree,
				Total:     inodes,
				Used:      inodesUsed,
			},
		},
	}

	klog.V(7).Infof("Volume: %s, VolumeMetrics: %s", req.VolumeId, resp.String())

	return &resp, nil
}
