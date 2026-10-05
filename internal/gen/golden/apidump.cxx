// apidump prints the results of the Hunspell API for a list of words: the
// reference output of the golden tests of gohunspell (internal/core).
//
// Build it against the Hunspell sources with the time limits of atypes.hxx
// raised (TIMELIMIT_* to one hour), so that the output does not depend on
// the speed of the machine; see README.md.
//
// With APIDUMP_OPS=1 in the environment, lines of the word list starting
// with "@" are operations on the dictionary instead of words (see ops below);
// APIDUMP_KEY sets the key of encrypted (hzip) dictionaries.
#include <hunspell.hxx>
#include <cstdlib>
#include <fstream>
#include <iostream>
#include <sstream>

static void print_trace(void*, int depth, const char* line) {
  std::cout << "  trace " << depth << " " << line << "\n";
}

static void dump(Hunspell& h, const std::string& w, const std::string& example,
                 const std::vector<std::string>& exmorph) {
  std::cout << "> " << w << "\n";
  int info = 0;
  std::string root;
  bool ok = h.spell(w, &info, &root);
  std::cout << "  spell " << ok << " " << info << " " << root << "\n";
  for (auto& s : h.suggest(w)) std::cout << "  sug " << s << "\n";
  std::vector<std::string> an = h.analyze(w);
  for (auto& s : an) std::cout << "  ana " << s << "\n";
  for (auto& s : h.stem(w)) std::cout << "  stem " << s << "\n";
  for (auto& s : h.stem(an)) std::cout << "  stemmorph " << s << "\n";
  for (auto& s : h.suffix_suggest(w)) std::cout << "  sfx " << s << "\n";
  if (!example.empty()) {
    for (auto& s : h.generate(w, example)) std::cout << "  gen " << s << "\n";
    for (auto& s : h.generate(w, exmorph)) std::cout << "  genmorph " << s << "\n";
  }
}

// op runs an operation line of the word list:
//   @add WORD                 add
//   @addflags WORD FLAGS [DESC] add_with_flags
//   @addaff WORD EXAMPLE      add_with_affix
//   @remove WORD              remove
//   @adddic PATH [KEY]        add_dic
//   @trace WORD               spell with the trace callback set
//   @gen WORD P1|P2...        generate with a list of morphological patterns
//   @stem D1|D2...            stem of a list of analyses
//   @iconv WORD               input_conv
//   @info                     encoding, wordchars, version and langnum
static void op(Hunspell& h, const std::string& line) {
  std::cout << line << "\n";
  std::istringstream in(line);
  std::string cmd, a, b, c;
  in >> cmd >> a >> b;
  std::getline(in >> std::ws, c);
  if (cmd == "@add") {
    std::cout << "  ret " << h.add(a) << "\n";
  } else if (cmd == "@addflags") {
    std::cout << "  ret " << h.add_with_flags(a, b, c) << "\n";
  } else if (cmd == "@addaff") {
    std::cout << "  ret " << h.add_with_affix(a, b) << "\n";
  } else if (cmd == "@remove") {
    std::cout << "  ret " << h.remove(a) << "\n";
  } else if (cmd == "@adddic") {
    std::cout << "  ret " << h.add_dic(a.c_str(), b.empty() ? nullptr : b.c_str()) << "\n";
  } else if (cmd == "@trace") {
    h.set_trace_callback(print_trace, nullptr);
    bool ok = h.spell(a);
    h.set_trace_callback(nullptr, nullptr);
    std::cout << "  spell " << ok << "\n";
  } else if (cmd == "@gen" || cmd == "@stem") {
    std::istringstream in2(line);
    std::string w, rest;
    in2 >> cmd;
    if (cmd == "@gen") in2 >> w;
    std::getline(in2 >> std::ws, rest);
    std::vector<std::string> pl;
    for (size_t i = 0; !rest.empty();) {
      size_t j = rest.find('|', i);
      pl.push_back(rest.substr(i, j == std::string::npos ? j : j - i));
      if (j == std::string::npos) break;
      i = j + 1;
    }
    std::vector<std::string> r = cmd == "@gen" ? h.generate(w, pl) : h.stem(pl);
    for (auto& s : r) std::cout << "  " << cmd.substr(1) << " " << s << "\n";
  } else if (cmd == "@iconv") {
    std::string dest;
    bool ok = h.input_conv(a, dest);
    std::cout << "  iconv " << ok << " " << dest << "\n";
  } else if (cmd == "@info") {
    std::cout << "  enc " << h.get_dict_encoding() << "\n";
    std::cout << "  wordchars " << h.get_wordchars_cpp() << "\n";
    std::cout << "  wordchars16";
    for (auto& wc : h.get_wordchars_utf16()) std::cout << " " << ((wc.h << 8) | wc.l);
    std::cout << "\n";
    std::cout << "  version " << h.get_version_cpp() << "\n";
    std::cout << "  langnum " << h.get_langnum() << "\n";
  } else {
    std::cout << "  unknown\n";
  }
}

int main(int argc, char** argv) {
  if (argc < 4) {
    std::cerr << "usage: apidump aff dic words [example]\n";
    return 1;
  }
  const char* key = std::getenv("APIDUMP_KEY");
  bool ops = std::getenv("APIDUMP_OPS") != nullptr;
  Hunspell h(argv[1], argv[2], key && *key ? key : nullptr);
  std::cerr << "--- loaded" << std::endl;
  std::string example = argc > 4 ? argv[4] : "";
  std::vector<std::string> exmorph = h.analyze(example);
  std::ifstream in(argv[3]);
  std::string w;
  while (std::getline(in, w)) {
    if (ops && !w.empty() && w[0] == '@')
      op(h, w);
    else
      dump(h, w, example, exmorph);
  }
}
