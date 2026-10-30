import Loading from "@/components/Skeleton";

export default function ProtectedLoading() {
  return (
    <>
      <div className="skeleton" style={{ width: 240, height: 28, marginBottom: 12 }} />
      <div className="card wide">
        <Loading rows={5} />
      </div>
    </>
  );
}
