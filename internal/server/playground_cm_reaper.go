package server

import (
	"context"
	"log"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Sprint 3 PR-2 (improvements.md #12). Ephemeral skill/soul ConfigMaps
// usually cascade-delete via the sandbox row's ON DELETE CASCADE. But
// when an `AgentSandbox` CRD is deleted out-of-band (`kubectl delete
// agentsandbox <name>` directly, no API call), the sandbox row stays
// and the ConfigMap is orphaned. Over time the workspace namespace
// accumulates dead `agentserver-draft-*` ConfigMaps.
//
// This reaper lives next to StartPlaygroundReaper. It scans every
// `agent-ws-*` namespace for ConfigMaps labeled
// `agentserver.io/ephemeral=true`, looks up the `agentserver.io/sandbox-id`
// label, and deletes the ConfigMap when no matching `sandboxes` row exists.

const cmReaperInterval = 5 * time.Minute

// StartConfigMapReaper spawns the ephemeral-ConfigMap garbage collector.
// No-op when NamespaceManager is nil (local dev without K8s).
func (s *Server) StartConfigMapReaper(ctx context.Context) {
	if s.NamespaceManager == nil {
		log.Printf("playground CM reaper: NamespaceManager unset, skipping")
		return
	}
	go func() {
		t := time.NewTicker(cmReaperInterval)
		defer t.Stop()
		s.reapOrphanConfigMaps(ctx) // run once on startup
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.reapOrphanConfigMaps(ctx)
			}
		}
	}()
	log.Printf("Playground ConfigMap reaper started (interval: %s)", cmReaperInterval)
}

func (s *Server) reapOrphanConfigMaps(ctx context.Context) {
	cs := s.NamespaceManager.Clientset()
	nsPrefix := s.NamespaceManager.Prefix() + "-"

	nsList, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "managed-by=agentserver",
	})
	if err != nil {
		log.Printf("playground CM reaper: list namespaces: %v", err)
		return
	}

	var orphans int
	for _, ns := range nsList.Items {
		if !strings.HasPrefix(ns.Name, nsPrefix) {
			continue
		}
		cms, err := cs.CoreV1().ConfigMaps(ns.Name).List(ctx, metav1.ListOptions{
			LabelSelector: "agentserver.io/ephemeral=true",
		})
		if err != nil {
			log.Printf("playground CM reaper: list CMs in %s: %v", ns.Name, err)
			continue
		}
		for _, cm := range cms.Items {
			sandboxID := cm.Labels["agentserver.io/sandbox-id"]
			if sandboxID == "" {
				continue // ephemeral but unlabeled — leave to a human to inspect
			}
			row, err := s.DB.GetSandbox(sandboxID)
			if err == nil && row != nil {
				continue // sandbox still tracked — CM is live
			}
			// row missing → orphan. Best-effort delete; log failures.
			if err := cs.CoreV1().ConfigMaps(ns.Name).Delete(ctx, cm.Name, metav1.DeleteOptions{}); err != nil {
				log.Printf("playground CM reaper: delete %s/%s: %v", ns.Name, cm.Name, err)
				continue
			}
			orphans++
			log.Printf("playground CM reaper: deleted orphan %s/%s (sandbox %s gone)", ns.Name, cm.Name, sandboxID)
		}
	}
	if orphans > 0 {
		log.Printf("playground CM reaper: removed %d orphan ConfigMap(s)", orphans)
	}
}
