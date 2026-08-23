export function ComingSoonPage({ domain }: { domain: string }) {
  return (
    <section className="content">
      <div className="hero">
        <div>
          <p className="eyebrow">NEXT DOMAIN</p>
          <h2>{domain} is not implemented yet.</h2>
          <p>
            The React shell and domain-router seam are ready. This page will receive its own shared
            schemas, oRPC contract, server router, and feature module.
          </p>
        </div>
      </div>
    </section>
  );
}
