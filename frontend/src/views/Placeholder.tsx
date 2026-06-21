import React from "react";

interface Props {
  icon: string;
  title: string;
  desc: string;
  features: string[];
}

export default function Placeholder({ icon, title, desc, features }: Props) {
  return (
    <div className="view">
      <div className="placeholder-scroll">
        <div className="placeholder-card">
          <div className="pc-icon">{icon}</div>
          <div className="coming-badge">Coming soon</div>
          <h2>{title}</h2>
          <p>{desc}</p>
          <div className="feature-list">
            {features.map((f, i) => (
              <div className="fl-item" key={i}>
                <span className="fl-dot">◆</span>
                <span>{f}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

export function MemoryView() {
  return (
    <Placeholder
      icon="🧠"
      title="Memory"
      desc="A graph-backed long-term memory and skills workspace for SuperAI."
      features={[
        "Browse and search the knowledge graph of people, facts and events",
        "Inspect what the agent has remembered from past conversations",
        "Manage installed skills and see when each one activates",
      ]}
    />
  );
}

export function DataView() {
  return (
    <Placeholder
      icon="🗄️"
      title="Data"
      desc="Import your documents and datasets so the agent can reason over them."
      features={[
        "Import files and folders into the vector store",
        "Connect data sources and run change-data-capture syncs",
        "Review chunking, embeddings and PII desensitization",
      ]}
    />
  );
}

export function LifeView() {
  return (
    <Placeholder
      icon="🌱"
      title="Life"
      desc="A personal assistant layer: schedules, records, people and reminders."
      features={[
        "Schedules and recurring routines the agent can act on",
        "Records and journals it keeps on your behalf",
        "People you know and reminders that fire at the right time",
      ]}
    />
  );
}
