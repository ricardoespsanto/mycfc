const feature = process.argv[2] ?? "requested feature";
console.error(`${feature} is not implemented in the current foundation slice.`);
process.exit(1);
