package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"launcher/internal/store"
)

// Esvaziar um nó: deixa de receber réplicas (manutenção) e as que tem
// mudam-se para outros, uma a uma, como num deploy -- a nova tem de passar
// no health check antes de a antiga sair do routing. Os addons não se
// mudam (os dados estão nesta máquina).

// DrainNode começa a esvaziar um nó; corre em fundo. O estado vê-se em
// NodeView.Drain.
func (s *Service) DrainNode(ctx context.Context, name string) error {
	if _, err := s.db.GetNode(ctx, name); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			return fmt.Errorf("%w: nó %s", ErrNotFound, name)
		}
		return err
	}
	if err := s.db.SetNodeSchedulable(ctx, name, false); err != nil {
		return err
	}
	if !s.rt.startNodeDrain(name) {
		return fmt.Errorf("%w: o nó %s já está a ser esvaziado", ErrConflict, name)
	}
	go s.drainNode(context.WithoutCancel(ctx), name)
	return nil
}

func (s *Service) drainNode(ctx context.Context, node string) {
	moved, err := s.moveReplicasOff(ctx, node)
	msg := fmt.Sprintf("esvaziado: %d réplica(s) mudada(s)", moved)
	if err != nil {
		msg = fmt.Sprintf("parou depois de %d réplica(s): %v", moved, err)
		s.logger.Error("esvaziando nó", "node", node, "err", err)
	} else {
		s.logger.Info("nó esvaziado", "node", node, "moved", moved)
	}
	s.rt.endNodeDrain(node, msg)
}

func (s *Service) moveReplicasOff(ctx context.Context, node string) (int, error) {
	containers, err := s.docker.ListContainers(ctx, false, map[string][]string{"label": {LabelRole + "=" + RoleApp}})
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, c := range containers {
		if nodeOf(c) != node || isPhantom(c) {
			continue
		}
		name := containerName(c)
		if _, draining := s.rt.drainDeadline(name); draining {
			continue
		}
		svc, err := s.db.GetService(ctx, c.Labels[LabelService])
		if err != nil || svc.CurrentRevisionID == nil || c.Labels[LabelRevision] != *svc.CurrentRevisionID {
			// Serviço apagado ou réplica de outra revisão: sai simplesmente.
			s.drainReplica(name)
			continue
		}
		if err := s.moveReplica(ctx, svc, name); err != nil {
			return moved, fmt.Errorf("%s: %w", name, err)
		}
		moved++
	}
	return moved, nil
}

// moveReplica cria uma réplica igual noutro nó, espera que fique pronta e
// só então tira a antiga. Durante isto o reconciliador não mexe no
// serviço (como num deploy).
func (s *Service) moveReplica(ctx context.Context, svc store.Service, old string) error {
	for !s.rt.startDeploy(svc.ID) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	defer s.rt.endDeploy(svc.ID)
	rev, err := s.db.GetRevision(ctx, *svc.CurrentRevisionID)
	if err != nil {
		return err
	}
	p, e, err := s.newScopeCache().get(ctx, svc.EnvironmentID)
	if err != nil {
		return err
	}
	env, err := s.containerEnv(ctx, e, rev)
	if err != nil {
		return err
	}
	name, err := s.createReplica(ctx, p, e, svc, rev, env)
	if err != nil {
		return err
	}
	if err := s.waitReady(ctx, []string{name}, rev); err != nil {
		s.stopAndRemove(context.WithoutCancel(ctx), name)
		return err
	}
	s.logger.Info("réplica mudada de nó", "service", svc.Slug, "from", old, "to", name)
	s.drainReplica(old)
	s.syncRouting(ctx)
	return nil
}
