package server

import (
	"context"
	"fmt"
	"regexp"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/proto"
	"github.com/satcomprojects/gonetem/internal/update"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

func (s *netemServer) ServerGetVersion(ctx context.Context, empty *empty.Empty) (*proto.VersionResponse, error) {
	return &proto.VersionResponse{
		Status: &proto.Status{
			Code: proto.StatusCode_OK,
		},
		Version: options.VERSION,
	}, nil
}

func (s *netemServer) ServerCleanContainers(ctx context.Context, empty *empty.Empty) (*proto.AckResponse, error) {
	client, err := s.getDockerClient()
	if err != nil {
		return nil, fmt.Errorf("unable to init docker client: %w", err)
	}

	cList, err := client.List(context.Background(), options.NETEM_ID)
	if err != nil {
		return nil, fmt.Errorf("unable to get container list: %w", err)
	}

	re := regexp.MustCompile(`^` + options.NETEM_ID + `(\w+)\.\w+`)
	g := new(errgroup.Group)
	g.SetLimit(maxConcurrentNodeTask)

	for _, cObj := range cList {
		groups := re.FindStringSubmatch(cObj.Name)
		if len(groups) == 2 && !s.projects.Has(groups[1]) {
			cObj := cObj

			g.Go(func() error {
				logrus.Debugf("Clean: remove container %s\n", cObj.Name)

				ctx := context.Background()
				if err := client.Stop(ctx, cObj.Container.ID); err != nil {
					return fmt.Errorf("unable to stop container %s: %w", cObj.Name, err)
				}
				if err := client.Rm(ctx, cObj.Container.ID); err != nil {
					return fmt.Errorf("unable to rm container %s: %w", cObj.Name, err)
				}

				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		return &proto.AckResponse{
			Status: &proto.Status{Code: proto.StatusCode_ERROR},
		}, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) ServerPullImages(empty *empty.Empty, stream proto.Netem_ServerPullImagesServer) error {
	imageTypes := s.config.PullImages

	client, err := s.getDockerClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	for _, imgT := range imageTypes {
		imgID := options.GetDockerImageId(imgT)

		if err := stream.Send(&proto.PullSrvMsg{
			Code:  proto.PullSrvMsg_START,
			Image: imgID,
		}); err != nil {
			return err
		}

		if err := client.ImagePull(ctx, imgID); err != nil {
			if sendErr := stream.Send(&proto.PullSrvMsg{
				Code:  proto.PullSrvMsg_ERROR,
				Image: imgID,
				Error: fmt.Sprintf("%s: %v", imgID, err),
			}); sendErr != nil {
				return sendErr
			}
			continue
		}
		if err := stream.Send(&proto.PullSrvMsg{
			Code:  proto.PullSrvMsg_OK,
			Image: imgID,
		}); err != nil {
			return err
		}
	}

	return nil
}

// updateStreamLogger forwards the progress of an update to the client.
type updateStreamLogger struct {
	stream proto.Netem_ServerUpdateServer
}

func (l *updateStreamLogger) Step(step, message string) {
	if err := l.stream.Send(&proto.UpdateSrvMsg{
		Code:    proto.UpdateSrvMsg_START,
		Step:    step,
		Message: message,
	}); err != nil {
		// Progress reporting is best effort: the update itself continues.
		logrus.Warnf("unable to send update step to client: %v", err)
	}
}

func (l *updateStreamLogger) Log(step, line string) {
	if err := l.stream.Send(&proto.UpdateSrvMsg{
		Code:    proto.UpdateSrvMsg_LOG,
		Step:    step,
		Message: line,
	}); err != nil {
		// Progress reporting is best effort: the update itself continues.
		logrus.Warnf("unable to send update log to client: %v", err)
	}
}

func (s *netemServer) ServerUpdate(empty *empty.Empty, stream proto.Netem_ServerUpdateServer) error {
	if !s.updateMu.TryLock() {
		return stream.Send(&proto.UpdateSrvMsg{
			Code:    proto.UpdateSrvMsg_ERROR,
			Message: update.ErrBusy.Error(),
		})
	}
	defer s.updateMu.Unlock()

	logger := &updateStreamLogger{stream: stream}
	updater := update.NewUpdater(logger, s.config.Update)

	if err := updater.Run(stream.Context()); err != nil {
		if sendErr := stream.Send(&proto.UpdateSrvMsg{
			Code:    proto.UpdateSrvMsg_ERROR,
			Message: err.Error(),
		}); sendErr != nil {
			return sendErr
		}
		return nil
	}

	if err := stream.Send(&proto.UpdateSrvMsg{
		Code:    proto.UpdateSrvMsg_OK,
		Step:    update.STEP_COMPLETE,
		Message: "gonetem has been updated, restart gonetem-server to use the new version",
	}); err != nil {
		return err
	}
	return nil
}
