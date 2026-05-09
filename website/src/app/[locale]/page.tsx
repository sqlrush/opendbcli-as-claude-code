import Nav from "@/components/Nav";
import Hero from "@/components/Hero";
import ThreeModes from "@/components/ThreeModes";
import Features from "@/components/Features";
import Footer from "@/components/Footer";

export default function Home() {
  return (
    <>
      <Nav />
      <main>
        <Hero />
        <div
          className="mx-auto h-px max-w-[1100px]"
          style={{ background: "var(--border)" }}
        />
        <ThreeModes />
        <div
          className="mx-auto h-px max-w-[1100px]"
          style={{ background: "var(--border)" }}
        />
        <Features />
      </main>
      <Footer />
    </>
  );
}
