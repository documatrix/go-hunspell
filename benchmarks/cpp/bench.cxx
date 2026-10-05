// bench measures the Hunspell C++ library on the same dictionary and word
// lists as the Go benchmarks (../compare_test.go and ../../bench_test.go).
//
//   g++ -O2 -std=c++17 -DHUNSPELL_STATIC -I$H/src/hunspell -include config.h \
//       -o bench bench.cxx $H/src/hunspell/*.cxx
//   ./bench ../../testdata/en_US/en_US ../../testdata/bench/en_US
//
// For the suggestion benchmark build Hunspell with the time limits of
// atypes.hxx raised (the Go benchmark uses WithoutTimeLimits).
#include <hunspell.hxx>
#include <chrono>
#include <cstdio>
#include <fstream>
#include <string>
#include <vector>

static std::vector<std::string> readWords(const std::string& path) {
  std::vector<std::string> res;
  std::ifstream in(path);
  std::string w;
  while (std::getline(in, w))
    if (!w.empty()) res.push_back(w);
  return res;
}

template <typename F>
static void bench(const char* name, int n, F f) {
  for (int i = 0; i < n / 10 + 1; i++) f(i);  // warm up
  auto start = std::chrono::steady_clock::now();
  for (int i = 0; i < n; i++) f(i);
  double ns = std::chrono::duration<double, std::nano>(std::chrono::steady_clock::now() - start).count();
  std::printf("%-18s %12.0f ns/op\n", name, ns / n);
}

int main(int argc, char** argv) {
  if (argc < 3) {
    std::fprintf(stderr, "usage: bench dictbase wordbase\n");
    return 1;
  }
  std::string aff = std::string(argv[1]) + ".aff", dic = std::string(argv[1]) + ".dic";
  auto good = readWords(std::string(argv[2]) + ".good");
  auto wrong = readWords(std::string(argv[2]) + ".wrong");
  bench("Load", 20, [&](int) { Hunspell h(aff.c_str(), dic.c_str()); });
  Hunspell h(aff.c_str(), dic.c_str());
  volatile size_t sink = 0;
  bench("SpellCorrect", 2000000, [&](int i) { sink += h.spell(good[i % good.size()]); });
  bench("SpellMisspelled", 500000, [&](int i) { sink += h.spell(wrong[i % wrong.size()]); });
  bench("Suggest", 1000, [&](int i) { sink += h.suggest(wrong[i % wrong.size()]).size(); });
  bench("Analyze", 500000, [&](int i) { sink += h.analyze(good[i % good.size()]).size(); });
  bench("Stem", 500000, [&](int i) { sink += h.stem(good[i % good.size()]).size(); });
  return 0;
}
